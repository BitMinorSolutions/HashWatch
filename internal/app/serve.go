package app

import (
	"errors"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

// AttachListener records the web listener this copy is serving on.
func (s *Server) AttachListener(ln net.Listener, host string) {
	s.lnMu.Lock()
	defer s.lnMu.Unlock()
	s.ln = ln
	s.Host = host
	s.LANOK = WildcardHost(host)
}

// ServeLoop serves the current listener and, after a rebind, the replacement.
// It returns when the listener is closed for shutdown (not for a rebind).
func (s *Server) ServeLoop() error {
	h := s.Handler()
	for {
		s.lnMu.Lock()
		ln := s.ln
		s.lnMu.Unlock()
		if ln == nil {
			return errors.New("no listener")
		}
		err := http.Serve(ln, h)
		s.lnMu.Lock()
		pending := s.rebindPending
		next := s.ln
		s.lnMu.Unlock()
		if pending {
			deadline := time.Now().Add(5 * time.Second)
			for pending && time.Now().Before(deadline) {
				time.Sleep(15 * time.Millisecond)
				s.lnMu.Lock()
				pending = s.rebindPending
				next = s.ln
				s.lnMu.Unlock()
			}
		}
		if next != nil && next != ln {
			continue
		}
		if err != nil && isClosedListener(err) {
			return nil
		}
		return err
	}
}

// Rebind closes the current web listener and binds the same port on host.
// The window and the Cloudflare tunnel keep using 127.0.0.1; only the
// wildcard (phones on Wi-Fi) changes. If the new bind fails, the previous
// address is restored when possible.
func (s *Server) Rebind(host string) error {
	if host == "" {
		host = "127.0.0.1"
	}
	s.lnMu.Lock()
	old := s.ln
	oldHost := s.Host
	port := s.Port
	if old == nil {
		s.Host = host
		s.LANOK = WildcardHost(host)
		s.lnMu.Unlock()
		return nil
	}
	s.rebindPending = true
	s.lnMu.Unlock()

	old.Close()

	newLn, bound, err := Listen(host, []int{port})
	if err != nil {
		if ln2, b2, err2 := Listen(oldHost, []int{port}); err2 == nil {
			s.lnMu.Lock()
			s.ln, s.Port, s.Host = ln2, b2, oldHost
			s.LANOK = WildcardHost(oldHost)
			s.rebindPending = false
			s.lnMu.Unlock()
			return err
		}
		s.lnMu.Lock()
		s.rebindPending = false
		s.lnMu.Unlock()
		return err
	}
	s.lnMu.Lock()
	s.ln, s.Port, s.Host = newLn, bound, host
	s.LANOK = WildcardHost(host)
	s.rebindPending = false
	s.lnMu.Unlock()
	if bound != port {
		log.Printf("rebinding to %s: preferred port %d was busy, using %d", host, port, bound)
	}
	return nil
}

func isClosedListener(err error) bool {
	return errors.Is(err, net.ErrClosed) || strings.Contains(err.Error(), "use of closed network connection")
}
