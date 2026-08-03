package cli

import "testing"

func TestParseGlobalFlagsAnywhere(t *testing.T) {
	invocation, err := Parse([]string{"add", "demo", "3000", "--json", "--force", "--verbose", "--all"})
	if err != nil {
		t.Fatal(err)
	}
	if invocation.Command != "add" || len(invocation.Args) != 2 {
		t.Fatalf("unexpected invocation: %#v", invocation)
	}
	if !invocation.Options.JSON || !invocation.Options.Force || !invocation.Options.Verbose || !invocation.Options.All {
		t.Fatalf("options were not parsed: %#v", invocation.Options)
	}
}

func TestParseShortcutAndAliases(t *testing.T) {
	tests := []struct {
		input   []string
		command string
	}{
		{[]string{"3000"}, "shortcut"},
		{[]string{"ls"}, "list"},
		{[]string{"rm", "demo"}, "remove"},
		{nil, "dashboard"},
		{[]string{"--version"}, "version"},
		{[]string{"-V"}, "version"},
	}
	for _, test := range tests {
		invocation, err := Parse(test.input)
		if err != nil {
			t.Fatalf("Parse(%v): %v", test.input, err)
		}
		if invocation.Command != test.command {
			t.Fatalf("Parse(%v) command = %q, want %q", test.input, invocation.Command, test.command)
		}
	}
}

func TestParseRejectsUnknownOption(t *testing.T) {
	if _, err := Parse([]string{"list", "--wat"}); err == nil {
		t.Fatal("expected unknown option error")
	}
}
