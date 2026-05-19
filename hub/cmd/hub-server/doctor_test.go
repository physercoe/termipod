package main

import (
	"net"
	"path/filepath"
	"testing"

	"github.com/termipod/hub/internal/server"
)

func TestParseDfAvail(t *testing.T) {
	out := "Filesystem 1024-blocks      Used Available Capacity Mounted on\n" +
		"/dev/disk1  500000000 100000000 400000000      20% /\n"
	got, err := parseDfAvail(out)
	if err != nil {
		t.Fatalf("parseDfAvail: %v", err)
	}
	if want := int64(400000000) * 1024; got != want {
		t.Fatalf("parseDfAvail = %d, want %d", got, want)
	}
	if _, err := parseDfAvail("garbage"); err == nil {
		t.Fatal("expected error on a one-line output")
	}
}

func TestCheckListenAddr(t *testing.T) {
	// A port nothing holds → available.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	free := ln.Addr().String()
	_ = ln.Close()
	if c := checkListenAddr(free); !c.OK {
		t.Fatalf("free address should be OK, got %+v", c)
	}

	// A held port → in-use, reported OK-with-note (not a failure).
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if c := checkListenAddr(held.Addr().String()); !c.OK {
		t.Fatalf("in-use address should be reported OK-with-note, got %+v", c)
	}
}

func TestCheckDataRootWritable(t *testing.T) {
	if c := checkDataRootWritable(t.TempDir()); !c.OK {
		t.Fatalf("temp dir should be writable, got %+v", c)
	}
}

func TestCheckDBReachable(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "hub.db")

	if c := checkDBReachable(dbPath); c.OK {
		t.Fatalf("missing DB should fail, got %+v", c)
	}
	if _, err := server.Init(dir, dbPath); err != nil {
		t.Fatalf("server.Init: %v", err)
	}
	if c := checkDBReachable(dbPath); !c.OK {
		t.Fatalf("initialised DB should be reachable, got %+v", c)
	}
}
