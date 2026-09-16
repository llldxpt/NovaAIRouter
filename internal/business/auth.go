package business

import (
	"encoding/json"
	"net"
	"net/http"
)

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

func writeAuthError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":   "unauthorized",
		"message": message,
	})
}

func (s *BusinessServer) requireInboundAccess(w http.ResponseWriter, r *http.Request) bool {
	mode := s.config.ShareMode
	if mode == "" {
		if s.config.DisableAdminAuth {
			mode = "public"
		} else {
			mode = "account"
		}
	}

	switch mode {
	case "public":
		return true
	case "local":
		if !isLocalRequest(r) {
			writeAuthError(w, http.StatusForbidden, "This endpoint only accepts local requests")
			return false
		}
		return true
	case "account":
		if isLocalRequest(r) {
			return true
		}
		apiKey := r.Header.Get("X-API-Key")
		if apiKey == "" || apiKey != s.config.APIKey {
			writeAuthError(w, http.StatusUnauthorized, "Invalid or missing API key")
			return false
		}
		return true
	default:
		if isLocalRequest(r) {
			return true
		}
		apiKey := r.Header.Get("X-API-Key")
		if apiKey == "" || apiKey != s.config.APIKey {
			writeAuthError(w, http.StatusUnauthorized, "Invalid or missing API key")
			return false
		}
		return true
	}
}
