package main

import (
	"archive/zip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/restic/restic/internal/global"
	rtest "github.com/restic/restic/internal/test"
)

func TestDumpSelectedPathsArchive(t *testing.T) {
	env, cleanup := withTestEnvironment(t)
	defer cleanup()
	testRunInit(t, env.gopts)
	files := map[string]string{"site/a.txt": "alpha", "site/nested/b.txt": "beta", "other/c.txt": "gamma", "other/site/a.txt": "different alpha"}
	for name, content := range files {
		filename := filepath.Join(env.testdata, filepath.FromSlash(name))
		rtest.OK(t, os.MkdirAll(filepath.Dir(filename), 0700))
		rtest.OK(t, os.WriteFile(filename, []byte(content), 0600))
	}
	rtest.OK(t, os.MkdirAll(filepath.Join(env.testdata, "site", "empty"), 0700))
	testRunBackup(t, "", []string{env.testdata}, BackupOptions{}, env.gopts)
	snapshot := testListSnapshots(t, env.gopts, 1)[0].String() + ":" + toPathInSnapshot(env.testdata)
	tests := []struct {
		name  string
		paths []string
		want  map[string]string
	}{
		{"multiple roots", []string{"/site", "/other"}, files},
		{"multiple files", []string{"/site/a.txt", "/other/site/a.txt"}, map[string]string{"site/a.txt": "alpha", "other/site/a.txt": "different alpha"}},
		{"file and directory", []string{"/site", "/other/c.txt"}, map[string]string{"site/a.txt": "alpha", "site/nested/b.txt": "beta", "other/c.txt": "gamma"}},
		{"same directory basename", []string{"/site", "/other/site"}, map[string]string{"site/a.txt": "alpha", "site/nested/b.txt": "beta", "other/site/a.txt": "different alpha"}},
		{"overlaps and duplicates", []string{"/site/nested", "/site", "/site/"}, map[string]string{"site/a.txt": "alpha", "site/nested/b.txt": "beta"}},
		{"nested selection preserves parents", []string{"/site/nested"}, map[string]string{"site/nested/b.txt": "beta"}},
		{"duplicate single file remains archive", []string{"/site/a.txt", "site/a.txt"}, map[string]string{"site/a.txt": "alpha"}},
		{"single file", []string{"/site/a.txt"}, map[string]string{"site/a.txt": "alpha"}},
		{"full tree", []string{"/"}, files},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			target := filepath.Join(t.TempDir(), "selected.zip")
			opts := DumpOptions{Archive: "zip", Target: target, ForceArchive: len(tc.paths) == 1}
			rtest.OK(t, withTermStatus(t, env.gopts, func(ctx context.Context, gopts global.Options) error {
				return runDump(ctx, opts, gopts, append([]string{snapshot}, tc.paths...), gopts.Term)
			}))
			z, err := zip.OpenReader(target)
			rtest.OK(t, err)
			defer z.Close()
			actual := make(map[string]string)
			seen := make(map[string]bool)
			for _, f := range z.File {
				if seen[f.Name] || strings.HasPrefix(f.Name, "/") || f.Name == "" {
					t.Fatalf("invalid or duplicate archive path %q", f.Name)
				}
				seen[f.Name] = true
				if f.FileInfo().IsDir() {
					continue
				}
				reader, err := f.Open()
				rtest.OK(t, err)
				content, err := io.ReadAll(reader)
				rtest.OK(t, err)
				rtest.OK(t, reader.Close())
				actual[f.Name] = string(content)
			}
			rtest.Equals(t, tc.want, actual)
		})
	}
	for _, paths := range [][]string{{"/site", "/missing"}, {"/site", "/site/missing"}, {"/site/a.txt", "/site/a.txt/child"}, {"/", "/missing"}, {""}} {
		t.Run("missing or invalid "+strings.Join(paths, " "), func(t *testing.T) {
			target := filepath.Join(t.TempDir(), "missing.zip")
			rtest.OK(t, os.WriteFile(target, []byte("existing output"), 0600))
			opts := DumpOptions{Archive: "zip", Target: target}
			err := withTermStatus(t, env.gopts, func(ctx context.Context, gopts global.Options) error {
				return runDump(ctx, opts, gopts, append([]string{snapshot}, paths...), gopts.Term)
			})
			rtest.Assert(t, err != nil, "invalid selection must fail")
			content, err := os.ReadFile(target)
			rtest.OK(t, err)
			rtest.Equals(t, "existing output", string(content))
		})
	}
	t.Run("legacy raw file", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "raw")
		opts := DumpOptions{Archive: "zip", Target: target}
		rtest.OK(t, withTermStatus(t, env.gopts, func(ctx context.Context, gopts global.Options) error {
			return runDump(ctx, opts, gopts, []string{snapshot, "/site/a.txt"}, gopts.Term)
		}))
		content, err := os.ReadFile(target)
		rtest.OK(t, err)
		rtest.Equals(t, "alpha", string(content))
	})
}
