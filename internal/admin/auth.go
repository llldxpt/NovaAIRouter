package admin

import (
	"encoding/json"
	"net"
	"net/http"
)

func (s *AdminServer) requireAPIKey(w http.ResponseWriter, r *http.Request) bool {
	if s.config.DisableAdminAuth {
		return true
	}

	apiKey := r.Header.Get("X-API-Key")
	if apiKey == "" || apiKey != s.config.APIKey {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{
			"error":   "unauthorized",
			"message": "Invalid or missing API key",
		})
		return false
	}
	return true
}

func isLocalRequest(r *http.Request) bool {
	remoteAddr := r.RemoteAddr
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}

	if host == "127.0.0.1" || host == "::1" || host == "localhost" {
		return true
	}

	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return true
	}

	return false
}

func (s *AdminServer) requireLocalOnly(w http.ResponseWriter, r *http.Request) bool {
	if !isLocalRequest(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{
			"error":   "forbidden",
			"message": "This endpoint only accepts local requests",
		})
		return false
	}
	return true
}

func (s *AdminServer) requireAPIKeyOrLocal(w http.ResponseWriter, r *http.Request) bool {
	if s.config.DisableAdminAuth {
		return true
	}
	if isLocalRequest(r) {
		return true
	}
	return s.requireAPIKey(w, r)
}
