package permission_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/harness/permission"
)

// project makes a root with a.txt, sub/b.txt, and the symlink key to a
// file outside the root. It returns the root and the folder outside.
func project(t *testing.T) (root, outside string) {
	t.Helper()
	root, outside = folders(t)
	require.NoError(t, os.Mkdir(filepath.Join(root, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "sub", "b.txt"), []byte("x"), 0o600))
	t.Setenv("HOME", outside)
	return root, outside
}

func TestReadOnlyCommandsRun(t *testing.T) {
	root, _ := project(t)
	commands := []string{
		"ls",
		"ls -la",
		"ls -la sub",
		"cat a.txt",
		`cat "a.txt" 'sub/b.txt'`,
		`cat a\ b.txt`,
		"cat < a.txt",
		"head -n 5 a.txt",
		"head -5 a.txt",
		"tail -n 20 a.txt",
		"wc -l a.txt sub/*.txt",
		"stat a.txt",
		"file a.txt",
		"diff a.txt sub/b.txt",
		"sort -u a.txt",
		"uniq -c a.txt",
		"cut -d , -f 1 a.txt",
		"tr a-z A-Z < a.txt",
		"grep -rn foo .",
		"grep -e foo -e bar a.txt",
		"grep --include=*.go -r foo sub",
		"rg foo sub",
		"rg -g '*.go' -i foo",
		"rg --files",
		"find . -name '*.go' -type f",
		`find sub -maxdepth 2 \( -name a -o -name b \) -print`,
		"find . -newer a.txt",
		"tree -L 2",
		"du -sh .",
		"pwd",
		"echo hi there",
		"git status",
		"git status --short",
		"git log --oneline -n 5",
		"git log -3 --stat -- sub",
		"git diff --stat HEAD~1",
		"git diff --cached",
		"git show HEAD",
		"git branch -a",
		"git rev-parse --show-toplevel",
		"git --no-pager log",
		"cat a.txt | grep x | wc -l",
		"ls && git status; pwd",
		"git status || true",
		"cd sub && ls",
		"cd sub && cat b.txt",
		"ls 2>/dev/null",
		"cat a.txt 2>&1 | head",
		"ls > /dev/null",
		"cat a.txt >/dev/null 2>/dev/null",
		"grep -f a.txt sub",
	}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			p := &permission.Policy{Dir: root}
			assert.Equal(t, ran, decide(t, p, bashCall(t, command)))
		})
	}
}

func TestOtherCommandsAsk(t *testing.T) {
	root, outside := project(t)
	require.NoError(t, os.Symlink(filepath.Join(outside, "a.txt"), filepath.Join(root, "key")))
	commands := []string{
		// A path outside the roots.
		"cat " + filepath.Join(outside, "a.txt"),
		"ls ..",
		"cat ../a.txt",
		"cat ~/a.txt",
		"ls ~",
		"cd .. && ls",
		"cd",
		"cat key",
		"cat *",
		"grep -r foo " + outside,
		"find " + outside + " -name x",
		"find . -newer " + filepath.Join(outside, "a.txt"),
		"cat < " + filepath.Join(outside, "a.txt"),
		"git diff a.txt " + filepath.Join(outside, "a.txt"),
		"cd sub && cat ../../a.txt",
		"grep -f " + filepath.Join(outside, "a.txt") + " sub",
		"rg --files " + outside,
		"echo " + outside + "/*",
		"ls -l ../",

		// A part that is not a read-only command.
		"rm a.txt",
		"cat a.txt; rm a.txt",
		"ls && git push",
		"sed -n 1p a.txt",
		"xargs cat",
		"bash -c ls",
		"/bin/cat a.txt",
		"git branch new",
		"git -C .. status",
		"git -c core.pager=x log",

		// A flag that the parser does not know, or a flag that writes,
		// runs a program, or follows a symlink.
		"ls --weird",
		"tail -f a.txt",
		"grep -R foo .",
		"rg --pre cat foo",
		"rg -L foo",
		"find -L .",
		"find . -exec rm {} ;",
		"find . -delete",
		"find . -fprint out.txt",
		"sort -o out.txt a.txt",
		"uniq a.txt out.txt",
		"git diff --output=x",
		"git diff --no-index a.txt sub/b.txt",
		"tree -o out.txt",
		"git log --output=x",
		"head -c",
		"cat a.txt | tee out.txt",

		// A redirect to a file.
		"echo x > out.txt",
		"echo x >> a.txt",
		"cat a.txt &> out.txt",
		"cat a.txt >& out.txt",
		"cat <<EOF",
		"ls >/dev/null2",
		"git status 2>&1 >out.txt",

		// A value that the parser cannot know.
		"cat $HOME/a.txt",
		`cat "$HOME/a.txt"`,
		"cat $(echo a.txt)",
		"cat `echo a.txt`",
		"FOO=1 cat a.txt",
		"cat ~root/a.txt",
		"cat .*/a.txt",
		"(ls)",
		"{ ls; }",
		"ls {a,b}",
		"ls # comment",
	}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			p := &permission.Policy{Dir: root}
			assert.Equal(t, asked, decide(t, p, bashCall(t, command)))
		})
	}
}

func TestReadOnlyCommandsInEachMode(t *testing.T) {
	root, outside := project(t)
	readIn := bashCall(t, "cat a.txt")
	readOut := bashCall(t, "cat "+filepath.Join(outside, "a.txt"))
	write := bashCall(t, "rm a.txt")
	tests := []struct {
		mode permission.Mode
		in   outcome
		out  outcome
		rm   outcome
	}{
		{mode: permission.Default, in: ran, out: asked, rm: asked},
		{mode: permission.AcceptEdits, in: ran, out: asked, rm: asked},
		{mode: permission.Plan, in: ran, out: asked, rm: denied},
		{mode: permission.Bypass, in: ran, out: ran, rm: ran},
	}
	for _, tt := range tests {
		t.Run(string(tt.mode), func(t *testing.T) {
			p := &permission.Policy{Mode: tt.mode, Dir: root}
			assert.Equal(t, tt.in, decide(t, p, readIn), "a read in the roots")
			assert.Equal(t, tt.out, decide(t, p, readOut), "a read outside the roots")
			assert.Equal(t, tt.rm, decide(t, p, write), "a command that is not read-only")
		})
	}
}

func TestRulesWinOverAReadOnlyCommand(t *testing.T) {
	root, _ := project(t)

	deny := &permission.Policy{Dir: root, Deny: []string{"bash(cat *)"}}
	assert.Equal(t, denied, decide(t, deny, bashCall(t, "ls && cat a.txt")))

	ask := &permission.Policy{Dir: root, Ask: []string{"bash(git *)"}}
	assert.Equal(t, asked, decide(t, ask, bashCall(t, "git status")))
}

func TestReadOnlyCommandsUseEachRoot(t *testing.T) {
	root, extra := project(t)
	p := &permission.Policy{Dir: root, Roots: []string{root, extra}}

	assert.Equal(t, ran, decide(t, p, bashCall(t, "cat "+filepath.Join(extra, "a.txt"))))
	assert.Equal(t, ran, decide(t, p, bashCall(t, "cd "+extra+" && cat a.txt")))
}

func TestBlockReadsOutsideAsksForACommandInBypass(t *testing.T) {
	root, outside := project(t)
	p := &permission.Policy{Mode: permission.Bypass, Dir: root, BlockReadsOutside: true}

	assert.Equal(t, asked, decide(t, p, bashCall(t, "cat "+filepath.Join(outside, "a.txt"))))
	assert.Equal(t, ran, decide(t, p, bashCall(t, "cat a.txt")))
	assert.Equal(t, ran, decide(t, p, bashCall(t, "rm a.txt")))
}
