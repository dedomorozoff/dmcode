package main

import (
	"strings"
	"testing"
)

// -s takes an optional value, which is the whole difficulty: Go's flag package
// demands an argument for a string flag and refuses one for a bool, so "the
// newest session" and "that session" have to come out of the arguments by hand.
//
// It matters because the id is opaque and the program only prints it on the way
// out. A user who wants the conversation they just had types -s and it works; a
// parser that swallowed -s as a bool would resume nothing and say nothing, which
// is the failure mode this table exists to rule out.

func TestTakeResumeFlag(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want []string
		res  resumeFlag
	}{
		{name: "absent", args: []string{"-C", "/tmp"}, want: []string{"-C", "/tmp"}},
		{name: "nothing at all", args: nil, want: nil},
		{
			name: "bare, means the newest session",
			args: []string{"-s"},
			want: nil,
			res:  resumeFlag{Asked: true},
		},
		{
			name: "with an id",
			args: []string{"-s", "sess-42"},
			want: nil,
			res:  resumeFlag{Asked: true, ID: "sess-42"},
		},
		{
			name: "long form with an id",
			args: []string{"--session", "sess-42"},
			want: nil,
			res:  resumeFlag{Asked: true, ID: "sess-42"},
		},
		{
			name: "long form, joined",
			args: []string{"--session=sess-42"},
			want: nil,
			res:  resumeFlag{Asked: true, ID: "sess-42"},
		},
		{
			name: "short form, joined",
			args: []string{"-s=sess-42"},
			want: nil,
			res:  resumeFlag{Asked: true, ID: "sess-42"},
		},
		{
			// The other flags keep working beside it. Reading "-C" as a session id
			// would resume nothing *and* ignore the directory.
			name: "a following flag is not the id",
			args: []string{"-s", "-C", "/tmp/project"},
			want: []string{"-C", "/tmp/project"},
			res:  resumeFlag{Asked: true},
		},
		{
			name: "keeps the other flags in order",
			args: []string{"-C", "/tmp", "-s", "sess-42", "-e"},
			want: []string{"-C", "/tmp", "-e"},
			res:  resumeFlag{Asked: true, ID: "sess-42"},
		},
		{
			name: "last one wins, as a flag does",
			args: []string{"-s", "sess-1", "--session=sess-2"},
			want: nil,
			res:  resumeFlag{Asked: true, ID: "sess-2"},
		},
		{
			// A flag that only looks like this one is left alone: "dmcode -see"
			// is not a session request.
			name: "a longer flag is untouched",
			args: []string{"-see"},
			want: []string{"-see"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rest, got := takeResumeFlag(tc.args)
			if got != tc.res {
				t.Errorf("resume = %+v, want %+v", got, tc.res)
			}
			if strings.Join(rest, " ") != strings.Join(tc.want, " ") {
				t.Errorf("rest = %v, want %v", rest, tc.want)
			}
		})
	}
}

// TestTakeResumeFlagDoesNotEatAPositional: the flag is taken out and everything
// else is handed back in order, because the editor subcommand and flag.Args() both
// depend on the rest still being what the user typed.
func TestTakeResumeFlagDoesNotEatAPositional(t *testing.T) {
	rest, res := takeResumeFlag([]string{"-s", "sess-42"})
	if len(rest) != 0 {
		t.Errorf("rest = %v, want nothing", rest)
	}
	if res.ID != "sess-42" {
		t.Errorf("id = %q, want sess-42", res.ID)
	}
}
