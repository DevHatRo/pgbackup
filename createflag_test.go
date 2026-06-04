package main

import (
	"slices"
	"testing"
)

func TestCreateFlag(t *testing.T) {
	on := engine{cfg: Config{IncludeCreateDatabase: true}}
	if got := on.createFlag(); !slices.Equal(got, []string{"--create"}) {
		t.Errorf("createFlag() with option on = %v, want [--create]", got)
	}

	off := engine{cfg: Config{IncludeCreateDatabase: false}}
	if got := off.createFlag(); len(got) != 0 {
		t.Errorf("createFlag() with option off = %v, want empty", got)
	}
}
