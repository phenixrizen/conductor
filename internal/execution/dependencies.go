package execution

import (
	"bytes"
	"context"
	"fmt"
	"strings"
)

func validateTree(ctx context.Context, dir, tree string) error {
	b, err := git(ctx, dir, nil, "ls-tree", "-rz", tree)
	if err != nil {
		return err
	}
	count := 0
	for _, entry := range bytes.Split(b, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		count++
		fields := bytes.SplitN(entry, []byte{'\t'}, 2)
		if count > MaxFiles || len(fields) != 2 || !safePath(string(fields[1])) || (!bytes.HasPrefix(fields[0], []byte("100644 blob ")) && !bytes.HasPrefix(fields[0], []byte("100755 blob "))) {
			return fmt.Errorf("%w: unsupported dependency tree path or mode", ErrInvalid)
		}
	}
	return nil
}

// Dependency outputs are cumulative diffs from the common original commit.
// Applying them serially would lose work or double-apply shared ancestors. Build
// verified temporary commits and let Git perform a real three-way tree merge.
func mergeDependencies(ctx context.Context, dir string, repo Repository, baseTree string) error {
	combined := repo.Commit
	for _, p := range repo.Dependencies {
		if p.BaseTree != baseTree {
			return fmt.Errorf("%w: dependency original tree mismatch", ErrInvalid)
		}
		if _, err := git(ctx, dir, nil, "read-tree", repo.Commit); err != nil {
			return err
		}
		if len(p.Patch) > 0 {
			if _, err := git(ctx, dir, p.Patch, "apply", "--cached", "--binary", "--whitespace=nowarn", "-"); err != nil {
				return fmt.Errorf("%w: dependency patch does not apply to its original tree", ErrInvalid)
			}
		}
		b, err := git(ctx, dir, nil, "write-tree")
		if err != nil {
			return err
		}
		tree := strings.TrimSpace(string(b))
		if tree != p.ResultTree {
			return fmt.Errorf("%w: dependency result tree mismatch", ErrInvalid)
		}
		if err = validateTree(ctx, dir, tree); err != nil {
			return err
		}
		b, err = git(ctx, dir, []byte("Synthetic dependency artifact\n"), "commit-tree", tree, "-p", repo.Commit)
		if err != nil {
			return err
		}
		incoming := strings.TrimSpace(string(b))
		b, err = git(ctx, dir, nil, "merge-tree", "--write-tree", "--no-messages", combined, incoming)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return ErrDependencyConflict
		}
		merged := strings.TrimSpace(string(b))
		if !oid.MatchString(merged) {
			return fmt.Errorf("%w: dependency merge result", ErrSandbox)
		}
		b, err = git(ctx, dir, []byte("Synthetic merged dependency artifacts\n"), "commit-tree", merged, "-p", combined, "-p", incoming)
		if err != nil {
			return err
		}
		combined = strings.TrimSpace(string(b))
	}
	if len(repo.Dependencies) > 0 {
		if _, err := git(ctx, dir, nil, "reset", "--hard", combined); err != nil {
			return err
		}
	}
	return nil
}

func validateProducerScope(before, after []file, allowed []string) error {
	old := make(map[string]file, len(before))
	for _, f := range before {
		old[f.Path] = f
	}
	for _, f := range after {
		original, ok := old[f.Path]
		if (!ok || original.Mode != f.Mode || !bytes.Equal(original.Content, f.Content)) && !allowedPath(f.Path, allowed) {
			return fmt.Errorf("%w: producer patch exceeds authorized writable paths", ErrSandbox)
		}
		delete(old, f.Path)
	}
	for name := range old {
		if !allowedPath(name, allowed) {
			return fmt.Errorf("%w: producer deletion exceeds authorized writable paths", ErrSandbox)
		}
	}
	return nil
}
