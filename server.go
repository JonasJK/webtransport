package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/webtransport-go"
	"golang.org/x/crypto/acme/autocert"
)

const (
	maxClients      = 100
	rateLimitPeriod = time.Second
	rateLimitMax    = 200
	sendBufSize     = 64
	broadcastHz     = 20
)

type Client struct {
	id      uint16
	session *webtransport.Session
	sendCh  chan []byte
	counter atomic.Int64
	resetAt atomic.Int64
}

func (c *Client) allowDatagram() bool {
	now := time.Now().UnixNano()
	if now > c.resetAt.Load() {
		c.counter.Store(0)
		c.resetAt.Store(now + int64(rateLimitPeriod))
	}
	return c.counter.Add(1) <= rateLimitMax
}

func (c *Client) sender() {
	for pkt := range c.sendCh {
		_ = c.session.SendDatagram(pkt)
	}
}

type clientList []*Client

var (
	clientsMu sync.Mutex
	clients   = map[*webtransport.Session]*Client{}
	snapshot  atomic.Pointer[clientList]

	freeIDs []uint16
	nextID  uint16 = 1

	staticContent []byte
	altSvcHeader  string
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

func releaseID(id uint16) {
	freeIDs = append(freeIDs, id)
}

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
		if c.session == exclude {
			continue
		}
		select {
		case c.sendCh <- packet:
		default:
		}
	}
}

type latestPos struct {
	mu  sync.Mutex
	pkt []byte
}

func main() {
	var err error
	staticContent, err = os.ReadFile("./static/index.html")
	if err != nil {
		log.Fatal("could not read static/index.html:", err)
	}

	domain := "webtransportdemo.duckdns.org"
	altSvcHeader = `h3=":443"; ma=86400`

	certManager := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(domain),
		Cache:      autocert.DirCache("./certs"),
	}

	h3TLSConfig := certManager.TLSConfig()
	h3TLSConfig.NextProtos = append(h3TLSConfig.NextProtos, "h3")

	mux := http.NewServeMux()

	wtServer := webtransport.Server{
		H3: &http3.Server{
			Addr:      ":443",
			TLSConfig: h3TLSConfig,
			Handler:   mux,
		},
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	webtransport.ConfigureHTTP3Server(wtServer.H3)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Alt-Svc", altSvcHeader)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(staticContent)
	})
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/wt", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("/wt hit: method=%s proto=%s", r.Method, r.Proto)

		clientsMu.Lock()
		if len(clients) >= maxClients {
			clientsMu.Unlock()
			http.Error(w, "server full", http.StatusServiceUnavailable)
			log.Println("rejected connection: server full")
			return
		}
		clientsMu.Unlock()

		session, err := wtServer.Upgrade(w, r)
		if err != nil {
			log.Printf("upgrade failed: %v", err)
			http.Error(w, "upgrade failed", http.StatusBadRequest)
			return
		}

		clientsMu.Lock()
		id := allocID()
		client := &Client{
			id:      id,
			session: session,
			sendCh:  make(chan []byte, sendBufSize),
		}
		client.resetAt.Store(time.Now().Add(rateLimitPeriod).UnixNano())
		clients[session] = client
		updateSnapshot()
		clientsMu.Unlock()

		log.Println("client connected:", id)
		go client.sender()
		go handleSession(client)
	})

	tcpMux := http.NewServeMux()
	tcpMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Alt-Svc", altSvcHeader)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(staticContent)
	})
	tcpMux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	go func() {
		log.Fatal(http.ListenAndServe(":80", certManager.HTTPHandler(nil)))
	}()
	go func() {
		s := &http.Server{
			Addr:      ":443",
			TLSConfig: certManager.TLSConfig(),
			Handler:   tcpMux,
		}
		log.Fatal(s.ListenAndServeTLS("", ""))
	}()

	log.Printf("Running on https://%s", domain)
	log.Fatal(wtServer.ListenAndServe())
}

func handleSession(c *Client) {
	defer func() {
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
		latest   pos
		hasPkt   atomic.Bool
		latestMu sync.Mutex
	)

	ticker := time.NewTicker(time.Second / broadcastHz)
	defer ticker.Stop()

	go func() {
		for range ticker.C {
			if !hasPkt.Load() {
				continue
			}
			latestMu.Lock()
			p := latest
			latestMu.Unlock()
			hasPkt.Store(false)

			out := []byte{
				0x01,
				uint8(c.id >> 8), uint8(c.id),
				uint8(p.x >> 8), uint8(p.x),
				uint8(p.y >> 8), uint8(p.y),
			}
			broadcast(out, c.session)
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
		case 0x02:
			if len(msg) < 9 {
				log.Printf("ping too short from client %d: %d bytes", c.id, len(msg))
				continue
			}
			select {
			case c.sendCh <- msg:
			default:
			}

		case 0x01:
			if len(msg) < 5 {
				continue
			}
			latestMu.Lock()
			latest = pos{
				x: uint16(msg[1])<<8 | uint16(msg[2]),
				y: uint16(msg[3])<<8 | uint16(msg[4]),
			}
			latestMu.Unlock()
			hasPkt.Store(true)
		}
	}
}
