package main

import "testing"

func TestSameOrigin(t *testing.T) {
	o := "http://127.0.0.1:48412"
	for u, want := range map[string]bool{
		o: true, o + "/": true, o + "/?shell=darwin": true, o + "#settings": true,
		"http://127.0.0.1:48413/": false, "http://127.0.0.1:4841/": false, "http://127.0.0.1:484120/": false,
		"http://localhost:48412/": false, "https://evil.trycloudflare.com/": false, "": false,
	} {
		if got := sameOrigin(u, o); got != want {
			t.Errorf("%q: %v", u, got)
		}
	}
	if sameOrigin(o, "") {
		t.Error("empty origin")
	}
}
