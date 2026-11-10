package main

import (
	"strings"
	"testing"
)

func TestRunRejectsInvalidMigrationArguments(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "unknown command", args: []string{"version"}, want: "usage:"},
		{name: "invalid direction", args: []string{"db", "migrate", "sideways"}, want: "usage:"},
		{name: "zero steps", args: []string{"db", "migrate", "down", "--steps", "0"}, want: "positive integer"},
		{name: "malformed steps", args: []string{"db", "migrate", "--steps"}, want: "usage:"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := run(test.args)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("run(%v) error = %v, want text %q", test.args, err, test.want)
			}
		})
	}
}
