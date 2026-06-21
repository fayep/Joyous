package main

import (
	"net/http"
	"testing"
)

// TestRegisterRoutes validates every ServeMux pattern at test time (HandleFunc panics on bad syntax).
func TestRegisterRoutes(t *testing.T) {
	registerRoutes(http.NewServeMux(), buildTestHub(t))
}
