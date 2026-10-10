package main

import (
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/sessionlink"
)

func TestParseOlderThan(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		want    time.Duration
		wantErr bool
	}{
		{name: "default", args: nil, want: sessionlink.DefaultOlderThan},
		{name: "explicit", args: []string{"--older-than", "30s"}, want: 30 * time.Second},
		{name: "negative", args: []string{"--older-than", "-1s"}, wantErr: true},
		{name: "invalid", args: []string{"--older-than", "soon"}, wantErr: true},
		{name: "unknown flag", args: []string{"--bogus"}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseOlderThan(tc.args)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
