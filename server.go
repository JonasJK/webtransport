package main

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/webtransport-go"
	"golang.org/x/crypto/acme/autocert"
)

const (
	maxClients      = 100
	rateLimitPeriod = time.Second
	rateLimitMax    = 200
)

type Client struct {
	id       uint16
	session  *webtransport.Session
	mu       sync.Mutex
	msgCount int
	resetAt  time.Time
}

func (c *Client) allowDatagram() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if now.After(c.resetAt) {
		c.msgCount = 0
		c.resetAt = now.Add(rateLimitPeriod)
	}
	if c.msgCount >= rateLimitMax {
		return false
	}
	c.msgCount++
	return true
}

var (
	clients   = map[*webtransport.Session]*Client{}
	clientsMu sync.Mutex
	freeIDs   []uint16
	nextID    uint16 = 1
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

func main() {
	domain := "webtransportdemo.duckdns.org"

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
		wtServer.H3.SetQUICHeaders(w.Header())
		http.FileServer(http.Dir("./static")).ServeHTTP(w, r)
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
		client := &Client{id: id, session: session, resetAt: time.Now().Add(rateLimitPeriod)}
		clients[session] = client
		clientsMu.Unlock()

		log.Println("client connected:", id)
		go handleSession(client)
	})

	tcpMux := http.NewServeMux()
	tcpMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		wtServer.H3.SetQUICHeaders(w.Header())
		http.FileServer(http.Dir("./static")).ServeHTTP(w, r)
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

func broadcast(packet []byte, exclude *webtransport.Session) {
	clientsMu.Lock()
	defer clientsMu.Unlock()
	for _, other := range clients {
		if other.session != exclude {
			_ = other.session.SendDatagram(packet)
		}
	}
}

func handleSession(c *Client) {
	defer func() {
		clientsMu.Lock()
		delete(clients, c.session)
		releaseID(c.id)
		clientsMu.Unlock()

		c.session.CloseWithError(0, "closed")
		log.Println("client disconnected:", c.id)
		broadcast([]byte{0xFF, uint8(c.id >> 8), uint8(c.id)}, c.session)
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
			_ = c.session.SendDatagram(msg)

		case 0x01:
			if len(msg) < 5 {
				continue
			}
			out := []byte{
				0x01,
				uint8(c.id >> 8), uint8(c.id),
				msg[1], msg[2], msg[3], msg[4],
			}
			broadcast(out, c.session)
		}
	}
}
