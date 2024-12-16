package server

import (
	"net/http"
)

type Server struct {
	addr string
	srv  *http.Server
}

func NewServer(addr string) *Server {
	return &Server{addr: addr}
}

func (s *Server) SetHandler(handler http.Handler) {
	s.srv = &http.Server{
		Addr:    s.addr,
		Handler: handler,
	}
}

func (s *Server) Start() error {
	return s.srv.ListenAndServe()
}

func (s *Server) Stop() error {
	if s.srv != nil {
		return s.srv.Close()
	}
	return nil
}
