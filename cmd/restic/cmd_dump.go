package main

import (
	"context"
	"fmt"
	"os"
	"path"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/debug"
	"github.com/restic/restic/internal/dump"
	"github.com/restic/restic/internal/errors"
	"github.com/restic/restic/internal/global"
	"github.com/restic/restic/internal/restic"
	"github.com/restic/restic/internal/ui"
	"github.com/restic/restic/internal/ui/progress"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func newDumpCommand(globalOptions *global.Options) *cobra.Command {
	var opts DumpOptions
	cmd := &cobra.Command{
		Use:   "dump [flags] snapshotID [file ...]",
		Short: "Print backed-up files or folders to stdout",
		Long: `
The "dump" command extracts files from a snapshot from the repository. If a
single file is selected, it prints its contents to stdout. Folders are output
as a tar (default) or zip file containing the contents of the specified folder.
Use "--force-archive" to wrap even a single file in the selected archive format.
Pass "/" as file name to dump the whole snapshot as an archive file.

If multiple files or directories are specified, they are collected into a single
archive (tar or zip). Paths remain relative to the selected snapshot root,
so "/dir" and "/other/dir" stay separate. Use the "snapshotID:subfolder"
syntax to choose the archive root explicitly.

The special snapshotID "latest" can be used to use the latest snapshot in the
repository.

To include the folder content at the root of the archive, you can use the
"snapshotID:subfolder" syntax, where "subfolder" is a path within the
snapshot tree as shown by "restic ls".

EXIT STATUS
===========

Exit status is 0 if the command was successful.
Exit status is 1 if there was any error.
Exit status is 10 if the repository does not exist.
Exit status is 11 if the repository is already locked.
Exit status is 12 if the password is incorrect.
`,
		GroupID:           cmdGroupDefault,
		DisableAutoGenTag: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			finalizeSnapshotFilter(&opts.SnapshotFilter)
			return runDump(cmd.Context(), opts, *globalOptions, args, globalOptions.Term)
		},
	}

	opts.AddFlags(cmd.Flags())
	return cmd
}

// DumpOptions collects all options for the dump command.
type DumpOptions struct {
	data.SnapshotFilter
	Archive      string
	Target       string
	ForceArchive bool
}

func (opts *DumpOptions) AddFlags(f *pflag.FlagSet) {
	initSingleSnapshotFilter(f, &opts.SnapshotFilter)
	f.StringVarP(&opts.Archive, "archive", "a", "tar", "set archive `format` as \"tar\" or \"zip\"")
	f.StringVarP(&opts.Target, "target", "t", "", "write the output to target `path`")
	f.BoolVar(&opts.ForceArchive, "force-archive", false, "write an archive even when selecting a single file")
}

func splitPath(p string) []string {
	d, f := path.Split(p)
	if d == "" || d == "/" {
		return []string{f}
	}
	s := splitPath(path.Join("/", d))
	return append(s, f)
}

// preparePathList splits each path into its components and drops paths that
// are contained within another path in the list. For example, the input
// ["dir", "dir/sub"] is reduced to ["dir"], because dumping "dir" already
// includes "dir/sub". If any of the paths is "/", the whole snapshot is dumped
// and the result is [[""]]. The order of the input is preserved.
func preparePathList(paths []string) [][]string {
	split := make([][]string, 0, len(paths))
	seen := make(map[string]struct{})
	for _, p := range paths {
		p = path.Clean(p)
		components := splitPath(p)
		if len(components) == 1 && components[0] == "" {
			return [][]string{{""}}
		}
		key := path.Join(components...)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		split = append(split, components)
	}

	filtered := make([][]string, 0, len(split))
	for _, components := range split {
		covered := false
		for length := 1; length < len(components); length++ {
			if _, ok := seen[path.Join(components[:length]...)]; ok {
				covered = true
				break
			}
		}
		if !covered {
			filtered = append(filtered, components)
		}
	}

	return filtered
}

// printFromTree resolves pathComponents within tree and returns the matching
// node with its Path set to the full path within the snapshot.
func printFromTree(ctx context.Context, tree data.TreeNodeIterator, repo restic.BlobLoader, prefix string, pathComponents []string) (*data.Node, error) {
	// If we print / we need to assume that there are multiple nodes at that
	// level in the tree.
	if pathComponents[0] == "" {
		return nil, fmt.Errorf("invalid path %q", prefix)
	}

	for it := range tree {
		if it.Error != nil {
			return nil, it.Error
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		node := it.Node
		// If dumping something in the highest level it will just take the
		// first item it finds and dump that according to the switch case below.
		if node.Name == pathComponents[0] {
			item := path.Join(prefix, pathComponents[0])
			l := len(pathComponents)
			switch {
			case l == 1 && (node.Type == data.NodeTypeFile || node.Type == data.NodeTypeDir || node.Type == data.NodeTypeSymlink):
				node.Path = item
				return node, nil
			case l > 1 && node.Type == data.NodeTypeDir:
				subtree, err := data.LoadTree(ctx, repo, *node.Subtree)
				if err != nil {
					return nil, errors.Wrapf(err, "cannot load subtree for %q", item)
				}
				return printFromTree(ctx, subtree, repo, item, pathComponents[1:])
			case l > 1:
				return nil, fmt.Errorf("%q should be a dir, but is a %q", item, node.Type)
			default:
				return nil, fmt.Errorf("%q should be a file, but is a %q", item, node.Type)
			}
		}
	}
	return nil, fmt.Errorf("path %q not found in snapshot", path.Join(prefix, pathComponents[0]))
}

func runDump(ctx context.Context, opts DumpOptions, gopts global.Options, args []string, term ui.Terminal) (resultErr error) {
	if len(args) < 2 {
		return errors.Fatal("no file and no snapshot ID specified")
	}
	switch opts.Archive {
	case "tar", "zip":
	default:
		return fmt.Errorf("unknown archive format %q", opts.Archive)
	}
	snapshotIDString, pathsToPrint := args[0], args[1:]
	for _, selectedPath := range pathsToPrint {
		if selectedPath == "" {
			return errors.Fatal("empty dump path; use / to select the snapshot root")
		}
	}
	printer := progress.NewTerminalPrinter(gopts.JSON, gopts.Verbosity, term)
	debug.Log("dump %d paths %v from %q", len(pathsToPrint), pathsToPrint, snapshotIDString)
	ctx, repo, unlock, err := openWithReadLock(ctx, gopts, gopts.NoLock, printer)
	if err != nil {
		return err
	}
	defer unlock()
	sn, subfolder, err := opts.SnapshotFilter.FindLatest(ctx, repo, repo, snapshotIDString)
	if err != nil {
		return errors.Fatalf("failed to find snapshot: %v", err)
	}
	if err = repo.LoadIndex(ctx, printer); err != nil {
		return err
	}
	sn.Tree, err = data.FindTreeDirectory(ctx, repo, sn.Tree, subfolder)
	if err != nil {
		return err
	}

	// Validate every requested path before filtering overlaps or opening the output.
	// Even /dir plus /dir/missing must fail instead of silently ignoring a selection.
	resolved := make(map[string]*data.Node)
	for _, selectedPath := range pathsToPrint {
		components := splitPath(path.Clean(selectedPath))
		key := path.Join(components...)
		if key == "" {
			continue
		}
		if _, ok := resolved[key]; ok {
			continue
		}
		// Tree iterators are single-use; each lookup needs a fresh iterator.
		tree, err := data.LoadTree(ctx, repo, *sn.Tree)
		if err != nil {
			return err
		}
		node, err := printFromTree(ctx, tree, repo, "/", components)
		if err != nil {
			return errors.Fatalf("cannot dump file: %v", err)
		}
		resolved[key] = node
	}
	selections := preparePathList(pathsToPrint)
	dumpAll := len(selections) == 1 && len(selections[0]) == 1 && selections[0][0] == ""
	roots := make([]*data.Node, 0, len(selections))
	if !dumpAll {
		for _, components := range selections {
			roots = append(roots, resolved[path.Join(components...)])
		}
	}

	outputFileWriter := term.OutputRaw()
	canWriteArchiveFunc := checkStdoutArchive(term)
	if opts.Target != "" {
		file, err := os.Create(opts.Target)
		if err != nil {
			return fmt.Errorf("cannot dump to file: %w", err)
		}
		defer func() {
			if err := file.Close(); resultErr == nil {
				resultErr = err
			}
		}()
		outputFileWriter = file
		canWriteArchiveFunc = func() error { return nil }
	}
	d := dump.New(opts.Archive, repo, outputFileWriter)
	if dumpAll {
		if err := canWriteArchiveFunc(); err != nil {
			return err
		}
		tree, err := data.LoadTree(ctx, repo, *sn.Tree)
		if err != nil {
			return err
		}
		return d.DumpTree(ctx, tree, "/")
	}
	// Preserve legacy raw output for one explicitly selected regular file only.
	if len(pathsToPrint) == 1 && len(roots) == 1 && roots[0].Type == data.NodeTypeFile && !opts.ForceArchive {
		return d.WriteNode(ctx, roots[0])
	}
	if err := canWriteArchiveFunc(); err != nil {
		return err
	}
	return d.DumpNodes(ctx, roots)
}

func checkStdoutArchive(term ui.Terminal) func() error {
	if term.OutputIsTerminal() {
		return func() error { return fmt.Errorf("stdout is the terminal, please redirect output") }
	}
	return func() error { return nil }
}
