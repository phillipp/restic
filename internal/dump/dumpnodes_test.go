package dump

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"testing"

	"github.com/restic/restic/internal/archiver"
	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/fs"
	rtest "github.com/restic/restic/internal/test"
)

// TestDumpNodesFullPaths dumps two directories and checks that the archive
// stores their contents at the full snapshot-relative paths.
func TestDumpNodesFullPaths(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	src := archiver.TestDir{
		"firstDir": archiver.TestDir{
			"file": archiver.TestFile{Content: "first"},
		},
		"secondDir": archiver.TestDir{
			"nested": archiver.TestDir{
				"file": archiver.TestFile{Content: "second"},
			},
		},
	}

	tmpdir, repo, _ := prepareTempdirRepoSrc(t, src)
	arch := archiver.New(repo, fs.Track{FS: fs.NewLocal()}, archiver.Options{})

	back := rtest.Chdir(t, tmpdir)
	defer back()

	sn, _, _, err := arch.Snapshot(ctx, []string{"."}, archiver.SnapshotOptions{})
	rtest.OK(t, err)

	tree, err := data.LoadTree(ctx, repo, *sn.Tree)
	rtest.OK(t, err)

	// Resolve firstDir and secondDir nodes with their full paths set.
	var roots []*data.Node
	for it := range tree {
		rtest.OK(t, it.Error)
		node := it.Node
		if node.Name == "firstDir" || node.Name == "secondDir" {
			node.Path = "/" + node.Name
			roots = append(roots, node)
		}
	}
	rtest.Assert(t, len(roots) == 2, "expected two roots, got %d", len(roots))

	dst := &bytes.Buffer{}
	d := New("tar", repo, dst)
	rtest.OK(t, d.DumpNodes(ctx, roots))

	want := []string{
		"firstDir/",
		"firstDir/file",
		"secondDir/",
		"secondDir/nested/",
		"secondDir/nested/file",
	}
	checkTarEntries(t, dst, want)
}

func checkTarEntries(t *testing.T, src *bytes.Buffer, want []string) {
	t.Helper()
	tr := tar.NewReader(src)

	var got []string
	for {
		hdr, err := tr.Next()
		if err != nil {
			break
		}
		got = append(got, hdr.Name)
	}
	sort.Strings(got)
	sort.Strings(want)
	rtest.Equals(t, want, got)
}

// Real customer restores exceed the classic ZIP limit of 65535 entries.
func TestDumpNodesZip64(t *testing.T) {
	const count = 65537
	roots := make([]*data.Node, count)
	for i := range roots {
		roots[i] = &data.Node{Type: data.NodeTypeSymlink, Mode: os.ModeSymlink | 0777, Path: fmt.Sprintf("/links/%06d", i), LinkTarget: "target"}
	}
	var output bytes.Buffer
	rtest.OK(t, New("zip", nil, &output).DumpNodes(context.Background(), roots))
	reader, err := zip.NewReader(bytes.NewReader(output.Bytes()), int64(output.Len()))
	rtest.OK(t, err)
	rtest.Equals(t, count, len(reader.File))
	for i, entry := range reader.File {
		rtest.Equals(t, fmt.Sprintf("links/%06d", i), entry.Name)
		rtest.Assert(t, entry.Mode()&os.ModeSymlink != 0, "symlink mode must survive")
		content, err := entry.Open()
		rtest.OK(t, err)
		data, err := io.ReadAll(content)
		rtest.OK(t, err)
		rtest.OK(t, content.Close())
		rtest.Equals(t, "target", string(data))
	}
}
