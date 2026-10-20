package main

import (
	"reflect"
	"testing"
)

func TestDispatchArgs(t *testing.T) {
	cases := []struct {
		argv0 string
		args  []string
		want  []string
	}{
		// 以 tinia 名字调用：完全不变
		{"/usr/local/bin/tinia", []string{"mcp", "file", "--host", "h"}, []string{"mcp", "file", "--host", "h"}},
		{"tinia", []string{"login"}, []string{"login"}},
		{`C:\Users\a\.tinia\bin\tinia.exe`, []string{"--version"}, []string{"--version"}},
		// 以 tinia-local-mcp 名字调用：前面补 mcp
		{"/Users/a/.tinia/bin/tinia-local-mcp", []string{"file", "--host", "h"}, []string{"mcp", "file", "--host", "h"}},
		{`C:\Users\a\.tinia\bin\Tinia-Local-MCP.EXE`, []string{"file"}, []string{"mcp", "file"}},
		{"tinia-local-mcp", nil, []string{"mcp"}},
		{"tinia-local-mcp", []string{"--help"}, []string{"mcp", "--help"}},
		// 版本留给根命令
		{"tinia-local-mcp", []string{"--version"}, []string{"--version"}},
		{"tinia-local-mcp.exe", []string{"-v"}, []string{"-v"}},
		// 只是名字里含有，不算
		{"/x/tinia-local-mcp-old", []string{"file"}, []string{"file"}},
	}
	for _, tc := range cases {
		if got := dispatchArgs(tc.argv0, tc.args); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("dispatchArgs(%q, %v) = %v, want %v", tc.argv0, tc.args, got, tc.want)
		}
	}
}
