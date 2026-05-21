package main

import "testing"

func TestLocalURLNormalizesWildcardAddress(t *testing.T) {
	got := localURL("[::]:8080")
	want := "http://localhost:8080"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestLocalURLWrapsIPv6Host(t *testing.T) {
	got := localURL("[2001:db8::1]:8080")
	want := "http://[2001:db8::1]:8080"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}
