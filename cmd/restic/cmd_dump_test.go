package main

import (
	"sort"
	"testing"

	rtest "github.com/restic/restic/internal/test"
)

func TestDumpSplitPath(t *testing.T) {
	testPaths := []struct {
		path   string
		result []string
	}{
		{"", []string{""}},
		{"test", []string{"test"}},
		{"test/dir", []string{"test", "dir"}},
		{"test/dir/sub", []string{"test", "dir", "sub"}},
		{"/", []string{""}},
		{"/test", []string{"test"}},
		{"/test/dir", []string{"test", "dir"}},
		{"/test/dir/sub", []string{"test", "dir", "sub"}},
	}
	for _, path := range testPaths {
		parts := splitPath(path.path)
		rtest.Equals(t, path.result, parts)
	}
}

func TestDumpPreparePathList(t *testing.T) {
	testPaths := []struct {
		paths  []string
		result [][]string
	}{
		{
			[]string{"test", "test/dir", "test/dir/sub"},
			[][]string{{"test"}},
		},
		{
			[]string{"/", "man", "doc", "doc/icons"},
			[][]string{{""}},
		},
		{
			[]string{"doc"},
			[][]string{{"doc"}},
		},
		{
			[]string{"man/", "doc", "doc/icons"},
			[][]string{{"man"}, {"doc"}},
		},
	}
	for _, path := range testPaths {
		parts := preparePathList(path.paths)
		sortSlice(parts)
		sortSlice(path.result)
		rtest.Equals(t, path.result, parts)
	}
}

func sortSlice(parts [][]string) {
	sort.Slice(parts, func(i, j int) bool {
		a, b := parts[i], parts[j]
		for k := 0; k < len(a) && k < len(b); k++ {
			if a[k] != b[k] {
				return a[k] < b[k]
			}
		}
		return len(a) < len(b)
	})
}
