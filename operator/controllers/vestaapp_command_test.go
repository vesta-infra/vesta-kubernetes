package controllers

import (
	"reflect"
	"testing"
)

func TestShellSplit(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"blnk", []string{"blnk"}},
		{"npm start", []string{"npm", "start"}},
		{"  npm   start  ", []string{"npm", "start"}},
		{`node --require "./tracing.js" server.js`, []string{"node", "--require", "./tracing.js", "server.js"}},
		{`sh -c 'echo hello world'`, []string{"sh", "-c", "echo hello world"}},
		{`echo "a b" c`, []string{"echo", "a b", "c"}},
		{`echo it\'s`, []string{"echo", "it's"}},
		{`echo ''`, []string{"echo", ""}},
	}
	for _, c := range cases {
		got := shellSplit(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("shellSplit(%q) = %#v, want %#v", c.in, got, c.want)
		}
	}
}
