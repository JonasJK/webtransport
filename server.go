package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/webtransport-go"
	"github.com/tdewolff/minify/v2"
	"github.com/tdewolff/minify/v2/css"
	"github.com/tdewolff/minify/v2/js"
	"golang.org/x/crypto/acme/autocert"
)

const (
	maxClients      = 100
	rateLimitPeriod = time.Second
	rateLimitMax    = 200
	sendBufSize     = 64
	broadcastHz     = 20

	domain = "webtransportdemo.duckdns.org"
	altSvc = `h3=":443"; ma=86400`
)

var mimeTypes = map[string]string{
	".html":  "text/html; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".js":    "application/javascript; charset=utf-8",
	".json":  "application/json",
	".wasm":  "application/wasm",
	".ico":   "image/x-icon",
	".png":   "image/png",
	".jpg":   "image/jpeg",
	".jpeg":  "image/jpeg",
	".svg":   "image/svg+xml",
	".woff":  "font/woff",
	".woff2": "font/woff2",
}

func mimeFor(path string) string {
	if ct, ok := mimeTypes[strings.ToLower(filepath.Ext(path))]; ok {
		return ct
	}
	return "application/octet-stream"
}

var minifier = func() *minify.M {
	m := minify.New()
	m.AddFunc("application/javascript", js.Minify)
	m.AddFunc("text/css", css.Minify)
	return m
}()

type cachedFile struct {
	plain       []byte
	gzipped     []byte
	etag        string
	contentType string
}

var fileCache = map[string]*cachedFile{}

func buildCache(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		ct := mimeFor(path)

		if ct == "application/javascript; charset=utf-8" || ct == "text/css; charset=utf-8" {
			mediaType := strings.SplitN(ct, ";", 2)[0]
			if minified, err := minifier.Bytes(mediaType, data); err == nil {
				data = minified
			} else {
				log.Printf("minify %s: %v", path, err)
			}
		}

		var gz []byte
		if strings.HasPrefix(ct, "text/") || strings.Contains(ct, "javascript") ||
			strings.Contains(ct, "json") || strings.Contains(ct, "wasm") {
			var buf bytes.Buffer
			w, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
			w.Write(data)
			w.Close()
			if buf.Len() < len(data) {
				gz = buf.Bytes()
			}
		}

		sum := sha256.Sum256(data)
		cf := &cachedFile{
			plain:       data,
			gzipped:     gz,
			etag:        `"` + hex.EncodeToString(sum[:8]) + `"`,
			contentType: ct,
		}
		rel, _ := filepath.Rel(filepath.Dir(root), path)
		urlPath := "/" + filepath.ToSlash(rel)
		fileCache[urlPath] = cf
		if urlPath == "/static/index.html" {
			fileCache["/"] = cf
		}
		return nil
	})
}

func serveFile(w http.ResponseWriter, r *http.Request) {
	cf := fileCache[r.URL.Path]
	if cf == nil {
		http.NotFound(w, r)
		return
	}

	h := w.Header()
	h.Set("Content-Type", cf.contentType)
	h.Set("ETag", cf.etag)
	h.Set("Cache-Control", "public, max-age=3600")

	if r.Header.Get("If-None-Match") == cf.etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	body := cf.plain
	if cf.gzipped != nil && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		h.Set("Content-Encoding", "gzip")
		h.Set("Vary", "Accept-Encoding")
		body = cf.gzipped
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.Write(body)
}

type Client struct {
	id      uint16
	session *webtransport.Session
	sendCh  chan []byte
	closing atomic.Bool
	rateMu  sync.Mutex
	counter int64
	resetAt time.Time
}

func (c *Client) allowDatagram() bool {
	now := time.Now()
	c.rateMu.Lock()
	defer c.rateMu.Unlock()
	if now.After(c.resetAt) {
		c.counter = 0
		c.resetAt = now.Add(rateLimitPeriod)
	}
	c.counter++
	return c.counter <= rateLimitMax
}

func (c *Client) sender() {
	for pkt := range c.sendCh {
		_ = c.session.SendDatagram(pkt)
	}
}

func trySend(ch chan []byte, pkt []byte) {
	defer func() { recover() }()
	select {
	case ch <- pkt:
	default:
	}
}

type clientList []*Client

var (
	clientsMu sync.Mutex
	clients   = map[*webtransport.Session]*Client{}
	snapshot  atomic.Pointer[clientList]

	freeIDs []uint16
	nextID  uint16 = 1
)

func allocID() uint16 {
	if len(freeIDs) > 0 {
		id := freeIDs[len(freeIDs)-1]
		freeIDs = freeIDs[:len(freeIDs)-1]
		return id
	}
	id := nextID
	nextID++
	return id
}

func releaseID(id uint16) { freeIDs = append(freeIDs, id) }

func updateSnapshot() {
	list := make(clientList, 0, len(clients))
	for _, c := range clients {
		list = append(list, c)
	}
	snapshot.Store(&list)
}

func broadcast(packet []byte, exclude *webtransport.Session) {
	list := snapshot.Load()
	if list == nil {
		return
	}
	for _, c := range *list {
		if c.session == exclude || c.closing.Load() {
			continue
		}
		trySend(c.sendCh, packet)
	}
}

func main() {
	if err := buildCache("./static"); err != nil {
		log.Fatal("loading static files:", err)
	}

	certManager := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(domain),
		Cache:      autocert.DirCache("./certs"),
	}

	h3TLS := certManager.TLSConfig()
	h3TLS.NextProtos = append(h3TLS.NextProtos, "h3")

	addAltSvc := func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Alt-Svc", altSvc)
			h.ServeHTTP(w, r)
		})
	}

	var wtServer webtransport.Server

	mux := http.NewServeMux()
	mux.HandleFunc("/", serveFile)
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/wt", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("/wt hit: method=%s proto=%s", r.Method, r.Proto)

		clientsMu.Lock()
		full := len(clients) >= maxClients
		clientsMu.Unlock()
		if full {
			http.Error(w, "server full", http.StatusServiceUnavailable)
			return
		}

		session, err := wtServer.Upgrade(w, r)
		if err != nil {
			log.Printf("upgrade failed: %v", err)
			return
		}

		clientsMu.Lock()
		id := allocID()
		c := &Client{
			id:      id,
			session: session,
			sendCh:  make(chan []byte, sendBufSize),
			resetAt: time.Now().Add(rateLimitPeriod),
		}
		clients[session] = c
		updateSnapshot()
		clientsMu.Unlock()

		log.Println("client connected:", id)
		go c.sender()
		go handleSession(c)
	})

	wtServer = webtransport.Server{
		H3: &http3.Server{
			Addr:      ":443",
			TLSConfig: h3TLS,
			Handler:   addAltSvc(mux),
		},
		CheckOrigin: func(*http.Request) bool { return true },
	}
	webtransport.ConfigureHTTP3Server(wtServer.H3)
	go func() { log.Fatal(http.ListenAndServe(":80", certManager.HTTPHandler(nil))) }()
	tcpLn, err := tls.Listen("tcp", ":443", h3TLS)
	if err != nil {
		log.Fatal("tcp listen:", err)
	}
	go func() {
		s := &http.Server{Handler: addAltSvc(mux)}
		log.Fatal(s.Serve(tcpLn))
	}()
	log.Printf("Listening on https://%s", domain)
	log.Fatal(wtServer.ListenAndServe())
}
func handleSession(c *Client) {
	done := make(chan struct{})

	defer func() {
		close(done)
		c.closing.Store(true)

		clientsMu.Lock()
		delete(clients, c.session)
		releaseID(c.id)
		updateSnapshot()
		clientsMu.Unlock()

		close(c.sendCh)
		c.session.CloseWithError(0, "closed")
		log.Println("client disconnected:", c.id)
		broadcast([]byte{0xFF, uint8(c.id >> 8), uint8(c.id)}, c.session)
	}()

	type pos struct{ x, y uint16 }
	var (
		latestMu sync.Mutex
		latest   pos
		hasPkt   bool
	)

	ticker := time.NewTicker(time.Second / broadcastHz)
	defer ticker.Stop()

	go func() {
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				latestMu.Lock()
				if !hasPkt {
					latestMu.Unlock()
					continue
				}
				p := latest
				hasPkt = false
				latestMu.Unlock()

				b := [7]byte{
					0x01,
					uint8(c.id >> 8), uint8(c.id),
					uint8(p.x >> 8), uint8(p.x),
					uint8(p.y >> 8), uint8(p.y),
				}
				broadcast(b[:], c.session)
			}
		}
	}()

	ctx := context.Background()
	for {
		msg, err := c.session.ReceiveDatagram(ctx)
		if err != nil {
			log.Println("read error:", err)
			return
		}
		if len(msg) == 0 {
			continue
		}
		if !c.allowDatagram() {
			log.Printf("rate limit: dropping datagram from client %d", c.id)
			continue
		}

		switch msg[0] {
		case 0x02: // ping — echo verbatim
			if len(msg) < 9 {
				continue
			}
			pkt := make([]byte, len(msg))
			copy(pkt, msg)
			select {
			case c.sendCh <- pkt:
			default:
			}

		case 0x01: // position update
			if len(msg) < 5 {
				continue
			}
			latestMu.Lock()
			latest = pos{
				x: uint16(msg[1])<<8 | uint16(msg[2]),
				y: uint16(msg[3])<<8 | uint16(msg[4]),
			}
			hasPkt = true
			latestMu.Unlock()
		}
	}
}
