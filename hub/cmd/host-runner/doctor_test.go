package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckHomeSet(t *testing.T) {
	t.Setenv("HOME", "/tmp/doctor-home")
	if c := checkHomeSet(); !c.OK {
		t.Fatalf("HOME set should pass, got %+v", c)
	}
	t.Setenv("HOME", "")
	if c := checkHomeSet(); c.OK {
		t.Fatalf("empty HOME should fail, got %+v", c)
	}
}

func TestCheckHubReachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/_info" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if c := checkHubReachable(context.Background(), srv.URL); !c.OK {
		t.Fatalf("hub answering /v1/_info should pass, got %+v", c)
	}
	// An unbound port → connection refused → fail.
	if c := checkHubReachable(context.Background(), "http://127.0.0.1:1"); c.OK {
		t.Fatalf("unreachable hub should fail, got %+v", c)
	}
}

func TestCheckHostToken(t *testing.T) {
	if c := checkHostToken(context.Background(), "http://127.0.0.1:1", "default", ""); c.OK {
		t.Fatalf("empty token should fail, got %+v", c)
	}

	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	}))
	defer srv.Close()

	if c := checkHostToken(context.Background(), srv.URL, "default", "tok"); !c.OK {
		t.Fatalf("accepted token should pass, got %+v", c)
	}
	status = http.StatusUnauthorized
	if c := checkHostToken(context.Background(), srv.URL, "default", "tok"); c.OK {
		t.Fatalf("rejected token should fail, got %+v", c)
	}
}

func TestCheckScratchWritable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if c := checkScratchWritable(); !c.OK {
		t.Fatalf("writable home should pass, got %+v", c)
	}
}

func TestCheckEngines(t *testing.T) {
	// CI may have zero engines installed — assert the check runs and is
	// well-formed rather than asserting a specific OK value.
	c := checkEngines(context.Background())
	if c.Name != "engines on PATH" {
		t.Fatalf("unexpected check name %q", c.Name)
	}
	if !c.OK && c.Hint == "" {
		t.Fatal("a failing engines check must carry a remediation hint")
	}
}
