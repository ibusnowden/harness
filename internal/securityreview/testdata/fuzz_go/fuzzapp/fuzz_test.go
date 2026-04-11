package fuzzapp

import "testing"

func FuzzCrash(f *testing.F) {
	f.Add([]byte("panic"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if string(data) == "panic" {
			panic("boom")
		}
	})
}
