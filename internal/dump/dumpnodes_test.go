package dump

import (
	"archive/tar"
	"bytes"
	"context"
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
