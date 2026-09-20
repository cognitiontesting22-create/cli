package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompletionHelp(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{
			name: "short help flag",
			args: []string{"foo", completionCommandName, "-h"},
		},
		{
			name: "long help flag",
			args: []string{"foo", completionCommandName, "--help"},
		},
		{
			name: "completion bash short help flag",
			args: []string{"foo", completionCommandName, "bash", "-h"},
		},
		{
			name: "completion bash long help flag",
			args: []string{"foo", completionCommandName, "bash", "--help"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			out := &bytes.Buffer{}

			cmd := &Command{
				EnableShellCompletion: true,
				Writer:                out,
				Flags: []Flag{
					&StringFlag{
						Name:     "required-flag",
						Required: true,
					},
				},
			}

			r := require.New(t)

			r.NoError(cmd.Run(buildTestContext(t), test.args))
			r.Contains(out.String(), "USAGE")
			r.NotContains(out.String(), "GLOBAL OPTIONS")
		})
	}
}

func TestCompletionDisable(t *testing.T) {
	cmd := &Command{}

	err := cmd.Run(buildTestContext(t), []string{"foo", completionCommandName})
	assert.Error(t, err, "Expected error for no help topic for completion")
}

func TestCompletionEnable(t *testing.T) {
	out := &bytes.Buffer{}

	cmd := &Command{
		EnableShellCompletion: true,
		Writer:                out,
		Flags: []Flag{
			&StringFlag{
				Name:     "goo",
				Required: true,
			},
		},
	}

	r := require.New(t)
	r.NoError(cmd.Run(buildTestContext(t), []string{"foo", completionCommandName}))
	r.Contains(out.String(), "USAGE")
}

func TestCompletionEnableDiffCommandName(t *testing.T) {
	out := &bytes.Buffer{}

	cmd := &Command{
		EnableShellCompletion:      true,
		ShellCompletionCommandName: "junky",
		Writer:                     out,
	}

	r := require.New(t)
	r.NoError(cmd.Run(buildTestContext(t), []string{"foo", "junky"}))
	r.Contains(out.String(), "USAGE")
}

func TestCompletionShell(t *testing.T) {
	for k := range shellCompletions {
		out := &bytes.Buffer{}

		t.Run(k, func(t *testing.T) {
			cmd := &Command{
				EnableShellCompletion: true,
				Writer:                out,
			}

			r := require.New(t)

			r.NoError(cmd.Run(buildTestContext(t), []string{"foo", completionCommandName, k}))
			r.NotEmpty(out.String(), "Expected non-empty completion output for shell %q", k)
		})
	}
}

func TestCompletionSubcommandOrder(t *testing.T) {
	// The completion subcommands must appear in a deterministic order so that
	// help output (and docs generated from it) does not change between runs.
	// Previously they were built by iterating a map, whose order Go randomizes.
	want := []string{"bash", "zsh", "fish", "pwsh"}

	// Build several times to guard against intra-process variation.
	for range 10 {
		cmd := buildCompletionCommand("foo")

		got := make([]string, 0, len(cmd.Commands))
		for _, sub := range cmd.Commands {
			got = append(got, sub.Name)
		}

		assert.Equal(t, want, got)
	}

	// Every shell in shellCompletions must be represented in the ordered list.
	assert.Len(t, completionShells, len(shellCompletions))
	for shell := range shellCompletions {
		assert.Contains(t, completionShells, shell)
	}
}

func TestCompletionBashNoShebang(t *testing.T) {
	// Regression test for https://github.com/urfave/cli/issues/2259
	// Bash completion scripts are sourced, not executed, so they must not
	// start with a `#!` shebang (flagged by Debian lintian as
	// `bash-completion-with-hashbang`).

	cmd := &Command{
		EnableShellCompletion: true,
	}

	r := require.New(t)

	bashRender := shellCompletions["bash"]
	r.NotNil(bashRender, "bash completion renderer should exist")

	output, err := bashRender(cmd, "myapp")
	r.NoError(err)
	r.NotEmpty(output, "bash completion output should not be empty")
	r.False(strings.HasPrefix(output, "#!"), "bash completion should not start with a shebang")
}

func TestCompletionBashAppendsSpace(t *testing.T) {
	// Regression test for https://github.com/urfave/cli/issues/2332
	// Do not register bash completions with `-o nospace`: after a command or
	// subcommand completion, Bash should append a space so the next word can be
	// completed without manually typing one.

	cmd := &Command{
		EnableShellCompletion: true,
	}

	r := require.New(t)

	bashRender := shellCompletions["bash"]
	r.NotNil(bashRender, "bash completion renderer should exist")

	output, err := bashRender(cmd, "myapp")
	r.NoError(err)
	r.NotContains(output, "-o nospace", "bash completion should append spaces after completed words")
	r.Contains(output, "complete -o bashdefault -o default -F __myapp_bash_autocomplete myapp")
}

func TestCompletionBashGreedyColonParsing(t *testing.T) {
	// Regression test for https://github.com/urfave/cli/issues/2335
	// The bash completion template uses fmt.Sprintf, so
	// literal "%" in the template must be escaped as "%%". The token
	// extraction must use the greedy ${line%%:*} (double %%) to split on
	// the *first* colon. A single % would use ${line%:*} which splits on
	// the *last* colon, breaking descriptions that contain colons
	// (e.g. "export:Export configs such as: compose-config").

	cmd := &Command{
		EnableShellCompletion: true,
	}

	r := require.New(t)

	bashRender := shellCompletions["bash"]
	r.NotNil(bashRender, "bash completion renderer should exist")

	output, err := bashRender(cmd, "myapp")
	r.NoError(err)

	// After fmt.Sprintf, the rendered script must contain ${line%%:*}
	// (greedy match) not ${line%:*} (non-greedy match).
	r.Contains(output, `${line%%:*}`, "token extraction should use greedy %% to match first colon")
	r.NotContains(output, `${line%:*}`, "token extraction must not use non-greedy single % (splits on last colon)")
}

func TestCompletionFishFormat(t *testing.T) {
	// Regression test for https://github.com/urfave/cli/issues/2285
	// Fish completion was broken due to incorrect format specifiers

	cmd := &Command{
		Name:                  "myapp",
		EnableShellCompletion: true,
	}

	r := require.New(t)

	// Test the fish shell completion renderer directly
	fishRender := shellCompletions["fish"]
	r.NotNil(fishRender, "fish completion renderer should exist")

	output, err := fishRender(cmd, "myapp")
	r.NoError(err)

	// Verify the function name is correctly formatted
	r.Contains(output, "function __myapp_perform_completion", "function name should contain app name")

	// Verify no format errors (like %! or (string=) which indicate broken fmt.Sprintf)
	r.NotContains(output, "%!", "output should not contain format errors")
	r.NotContains(output, "(string=", "output should not contain invalid fish syntax")

	// Verify the complete commands reference the app correctly
	r.Contains(output, "complete -c myapp", "complete command should reference app name")
	r.Contains(output, "(__myapp_perform_completion)", "completion function should be registered")
}

func TestCompletionFishOmitsPositionalTokenFromDynamicCompletion(t *testing.T) {
	cmd := &Command{
		Name:                  "myapp",
		EnableShellCompletion: true,
	}

	r := require.New(t)

	fishRender := shellCompletions["fish"]
	r.NotNil(fishRender, "fish completion renderer should exist")

	output, err := fishRender(cmd, "myapp")
	r.NoError(err)

	r.Contains(output, `if string match -q -- "-*" $lastArg`)
	r.Contains(output, "set results ($args[1] $args[2..-1] $lastArg --generate-shell-completion 2> /dev/null)")
	r.Contains(output, "set results ($args[1] $args[2..-1] --generate-shell-completion 2> /dev/null)")
}

func TestCompletionBashOmitsPositionalTokenFromDynamicCompletion(t *testing.T) {
	cmd := &Command{
		Name:                  "myapp",
		EnableShellCompletion: true,
	}

	r := require.New(t)

	bashRender := shellCompletions["bash"]
	r.NotNil(bashRender, "bash completion renderer should exist")

	output, err := bashRender(cmd, "myapp")
	r.NoError(err)

	r.Contains(output, `if [[ "${current_word}" == "-"* ]]; then`)
	r.Contains(output, `printf '%s %s --generate-shell-completion' "${words_before_cursor[*]}" "${current_word}"`)
	r.Contains(output, `printf '%s --generate-shell-completion' "${words_before_cursor[*]}"`)
}

func TestCompletionSubcommand(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		contains    string
		msg         string
		msgArgs     []any
		notContains bool
		// wantNoAction asserts that the command action must not run, even
		// though shell completion is requested (https://github.com/urfave/cli/issues/1993).
		wantNoAction bool
	}{
		{
			name:     "subcommand general completion",
			args:     []string{"foo", "bar", completionFlag},
			contains: "xyz",
			msg:      "Expected output to contain shell name %[1]q",
			msgArgs: []any{
				"xyz",
			},
		},
		{
			name:     "subcommand flag completion",
			args:     []string{"foo", "bar", "-", completionFlag},
			contains: "l1",
			msg:      "Expected output to contain shell name %[1]q",
			msgArgs: []any{
				"l1",
			},
		},
		{
			name:     "subcommand double dash shows long flags",
			args:     []string{"foo", "bar", "--", completionFlag},
			contains: "--l1",
			msg:      "Expected output to contain flag %[1]q",
			msgArgs: []any{
				"--l1",
			},
		},
		{
			name:     "sub sub command general completion",
			args:     []string{"foo", "bar", "xyz", completionFlag},
			contains: "-g",
			msg:      "Expected output to contain flag %[1]q",
			msgArgs: []any{
				"-g",
			},
			notContains: true,
		},
		{
			name:     "sub sub command flag completion",
			args:     []string{"foo", "bar", "xyz", "-", completionFlag},
			contains: "-g",
			msg:      "Expected output to contain flag %[1]q",
			msgArgs: []any{
				"-g",
			},
		},
		{
			name:     "sub sub command double dash shows flags",
			args:     []string{"foo", "bar", "xyz", "--", completionFlag},
			contains: "--help",
			msg:      "Expected output to contain flag %[1]q",
			msgArgs: []any{
				"--help",
			},
		},
		{
			name:     "sub sub command no completion extra args",
			args:     []string{"foo", "bar", "xyz", "--", "sargs", completionFlag},
			contains: "-g",
			msg:      "Expected output to contain flag %[1]q",
			msgArgs: []any{
				"-g",
			},
			notContains:  true,
			wantNoAction: true,
		},
		{
			name:     "subcommand partial double dash flag completion",
			args:     []string{"foo", "bar", "--l", completionFlag},
			contains: "--l1",
			msg:      "Expected output to contain flag %[1]q",
			msgArgs: []any{
				"--l1",
			},
		},
		{
			name:     "sub sub command partial double dash flag completion",
			args:     []string{"foo", "bar", "xyz", "--he", completionFlag},
			contains: "--help",
			msg:      "Expected output to contain flag %[1]q",
			msgArgs: []any{
				"--help",
			},
		},
		{
			name:     "subcommand partial flag completion after positional arg",
			args:     []string{"foo", "bar", "value", "--l", completionFlag},
			contains: "--l1",
			msg:      "Expected output to contain flag %[1]q",
			msgArgs: []any{
				"--l1",
			},
			wantNoAction: true,
		},
		{
			name:     "subcommand flag completion after positional arg",
			args:     []string{"foo", "bar", "value", "-", completionFlag},
			contains: "--l1",
			msg:      "Expected output to contain flag %[1]q",
			msgArgs: []any{
				"--l1",
			},
			wantNoAction: true,
		},
		{
			name:     "sub sub command partial flag completion after positional args",
			args:     []string{"foo", "bar", "xyz", "first", "second", "--he", completionFlag},
			contains: "--help",
			msg:      "Expected output to contain flag %[1]q",
			msgArgs: []any{
				"--help",
			},
			wantNoAction: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			out := &bytes.Buffer{}
			actionRan := false

			cmd := &Command{
				EnableShellCompletion: true,
				Writer:                out,
				Commands: []*Command{
					{
						Name: "bar",
						Flags: []Flag{
							&StringFlag{
								Name: "l1",
							},
						},
						Action: func(ctx context.Context, c *Command) error {
							actionRan = true
							return nil
						},
						Commands: []*Command{
							{
								Name: "xyz",
								Flags: []Flag{
									&StringFlag{
										Name: "g",
										Aliases: []string{
											"t",
										},
									},
								},
								Action: func(ctx context.Context, c *Command) error {
									actionRan = true
									return nil
								},
							},
						},
					},
				},
			}

			r := require.New(t)

			r.NoError(cmd.Run(buildTestContext(t), test.args))
			if test.notContains {
				r.NotContainsf(out.String(), test.contains, test.msg, test.msgArgs...)
			} else {
				r.Containsf(out.String(), test.contains, test.msg, test.msgArgs...)
			}
			if test.wantNoAction {
				r.False(actionRan, "command action must not run for a completion request")
			}
		})
	}
}

func TestCompletionPartialFlagAfterPositionalArgs(t *testing.T) {
	// Regression test: completing an unfinished flag (e.g. `--pa<TAB>`)
	// must suggest matching flags regardless of how many positional
	// arguments precede it, at any command depth.

	tests := []struct {
		name     string
		args     []string
		expected string
	}{
		{
			name:     "subcommand partial flag no positional args",
			args:     []string{"command", "subcommand", "--pa", completionFlag},
			expected: "--par1\n--par2\n",
		},
		{
			name:     "subcommand partial flag one positional arg",
			args:     []string{"command", "subcommand", "value", "--pa", completionFlag},
			expected: "--par1\n--par2\n",
		},
		{
			name:     "subcommand partial flag two positional args",
			args:     []string{"command", "subcommand", "first", "second", "--pa", completionFlag},
			expected: "--par1\n--par2\n",
		},
		{
			name:     "subcommand single dash lists visible flags after positional arg",
			args:     []string{"command", "subcommand", "value", "-", completionFlag},
			expected: "--par1\n--par2\n--flag\n--help:show help\n",
		},
		{
			name:     "nested command partial flag after positional arg",
			args:     []string{"command", "subcommand", "nested", "value", "--pa", completionFlag},
			expected: "--par1\n--par2\n",
		},
		{
			name:     "root partial flag after positional arg",
			args:     []string{"command", "value", "--ro", completionFlag},
			expected: "--root-flag\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			out := &bytes.Buffer{}
			actionRan := false

			subFlags := []Flag{
				&StringFlag{Name: "par1"},
				&StringFlag{Name: "par2"},
				&BoolFlag{Name: "flag"},
				&BoolFlag{Name: "secret", Hidden: true},
			}

			cmd := &Command{
				Name:                  "command",
				EnableShellCompletion: true,
				Writer:                out,
				Flags: []Flag{
					&BoolFlag{Name: "root-flag"},
				},
				Commands: []*Command{
					{
						Name:  "subcommand",
						Flags: subFlags,
						Action: func(ctx context.Context, c *Command) error {
							actionRan = true
							return nil
						},
						Commands: []*Command{
							{
								Name:  "nested",
								Flags: subFlags,
								Action: func(ctx context.Context, c *Command) error {
									actionRan = true
									return nil
								},
							},
						},
					},
				},
			}

			r := require.New(t)
			// root-level completion reads os.Args verbatim
			origArgv := os.Args
			os.Args = test.args
			t.Cleanup(func() { os.Args = origArgv })
			r.NoError(cmd.Run(buildTestContext(t), test.args))
			r.Equal(test.expected, out.String())
			r.False(actionRan, "command action must not run for a completion request")
		})
	}
}

func TestCompletionPartialFlagAfterPositionalArgsCustomShellComplete(t *testing.T) {
	// A custom ShellComplete handler still receives the arguments
	// (positional args plus the partial flag) unchanged.
	out := &bytes.Buffer{}

	var gotArgs []string
	cmd := &Command{
		Name:                  "command",
		EnableShellCompletion: true,
		Writer:                out,
		Commands: []*Command{
			{
				Name: "subcommand",
				ShellComplete: func(ctx context.Context, c *Command) {
					gotArgs = c.Args().Slice()
				},
			},
		},
	}

	r := require.New(t)
	r.NoError(cmd.Run(buildTestContext(t), []string{"command", "subcommand", "value", "--pa", completionFlag}))
	r.Equal([]string{"value", "--pa"}, gotArgs)
}

func TestCompletionPartialFlagAfterPositionalArgsDisabled(t *testing.T) {
	// With shell completion disabled, an unknown --pa flag is still an error.
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}

	cmd := &Command{
		Name:      "command",
		Writer:    out,
		ErrWriter: errOut,
		Commands: []*Command{
			{
				Name: "subcommand",
				Flags: []Flag{
					&StringFlag{Name: "par1"},
				},
				Action: func(ctx context.Context, c *Command) error {
					return nil
				},
			},
		},
	}

	r := require.New(t)
	err := cmd.Run(buildTestContext(t), []string{"command", "subcommand", "value", "--pa", completionFlag})
	r.Error(err)
	r.Contains(err.Error(), "flag provided but not defined")
}

func TestCompletionAfterDoubleDashNeverRunsAction(t *testing.T) {
	// Regression test for https://github.com/urfave/cli/issues/1993:
	// pressing tab on a command line that holds a "--" must never execute
	// the command action, and nothing is suggested past the "--" because
	// only positional arguments are accepted after it.

	tests := []struct {
		name string
		args []string
	}{
		{
			name: "root command past double dash",
			args: []string{"foo", "--", "somearg", completionFlag},
		},
		{
			name: "root command past double dash multiple words",
			args: []string{"foo", "--", "bar", "baz", completionFlag},
		},
		{
			name: "subcommand past double dash",
			args: []string{"foo", "sub", "--", "somearg", completionFlag},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			out := &bytes.Buffer{}
			actionRan := false

			cmd := &Command{
				EnableShellCompletion: true,
				Writer:                out,
				Action: func(ctx context.Context, c *Command) error {
					actionRan = true
					return nil
				},
				Commands: []*Command{
					{
						Name: "sub",
						Action: func(ctx context.Context, c *Command) error {
							actionRan = true
							return nil
						},
					},
				},
			}

			r := require.New(t)
			r.NoError(cmd.Run(buildTestContext(t), test.args))
			r.Empty(out.String(), "no suggestions expected past a --")
			r.False(actionRan, "command action must not run for a completion request")
		})
	}
}

func TestCompletionAfterDoubleDashDoesNotLeakToNextRun(t *testing.T) {
	// A Command answering several completion requests must answer each one
	// on its own terms: a request past a "--" must not make the next
	// request behave as though it were past one too.
	origArgv := os.Args
	t.Cleanup(func() { os.Args = origArgv })

	out := &bytes.Buffer{}

	cmd := &Command{
		EnableShellCompletion: true,
		Writer:                out,
		Flags: []Flag{
			&BoolFlag{
				Name: "verbose",
			},
		},
		Commands: []*Command{
			{
				Name: "sub",
			},
		},
	}

	r := require.New(t)

	os.Args = []string{"foo", "--", "somearg"}
	r.NoError(cmd.Run(buildTestContext(t), []string{"foo", "--", "somearg", completionFlag}))
	r.Empty(out.String())

	out.Reset()
	os.Args = []string{"foo", "-", completionFlag}
	r.NoError(cmd.Run(buildTestContext(t), []string{"foo", "-", completionFlag}))
	r.Contains(out.String(), "-verbose")
}

func TestCompletionSubcommandCustomShellComplete(t *testing.T) {
	out := &bytes.Buffer{}

	cmd := &Command{
		EnableShellCompletion: true,
		Writer:                out,
		Commands: []*Command{
			{
				Name: "index",
				Commands: []*Command{
					{
						Name: "show",
						ShellComplete: func(ctx context.Context, cmd *Command) {
							fmt.Fprintln(cmd.Root().Writer, "custom-index")
						},
						Action: func(ctx context.Context, cmd *Command) error { return nil },
					},
				},
			},
		},
	}

	r := require.New(t)
	r.NoError(cmd.Run(buildTestContext(t), []string{"foo", "index", "show", completionFlag}))
	r.Equal("custom-index\n", out.String())
}

func TestCompletionRunsBeforeChain(t *testing.T) {
	type contextKey struct{}

	out := &bytes.Buffer{}
	cmd := &Command{
		EnableShellCompletion: true,
		Writer:                out,
		Before: func(ctx context.Context, cmd *Command) (context.Context, error) {
			return context.WithValue(ctx, contextKey{}, "ready"), nil
		},
		Commands: []*Command{
			{
				Name: "index",
				Commands: []*Command{
					{
						Name: "show",
						ShellComplete: func(ctx context.Context, cmd *Command) {
							fmt.Fprintln(cmd.Root().Writer, ctx.Value(contextKey{}))
						},
						Action: func(ctx context.Context, cmd *Command) error { return nil },
					},
				},
			},
		},
	}

	r := require.New(t)
	r.NoError(cmd.Run(buildTestContext(t), []string{"foo", "index", "show", completionFlag}))
	r.Equal("ready\n", out.String())
}

func TestCompletionReturnsBeforeError(t *testing.T) {
	beforeErr := errors.New("load config")
	completed := false

	cmd := &Command{
		EnableShellCompletion: true,
		Writer:                io.Discard,
		Before: func(ctx context.Context, cmd *Command) (context.Context, error) {
			return nil, beforeErr
		},
		ShellComplete: func(ctx context.Context, cmd *Command) {
			completed = true
		},
	}

	err := cmd.Run(buildTestContext(t), []string{"foo", completionFlag})

	require.ErrorIs(t, err, beforeErr)
	assert.False(t, completed)
}

func TestCompletionInvalidShell(t *testing.T) {
	cmd := &Command{
		EnableShellCompletion: true,
	}

	unknownShellName := "junky-sheell"
	err := cmd.Run(buildTestContext(t), []string{"foo", completionCommandName, unknownShellName})
	assert.ErrorContains(t, err, fmt.Sprintf("No help topic for '%s'", unknownShellName))
}

func TestCompletionShellRenderError(t *testing.T) {
	unknownShellName := "junky-sheell"

	enableError := true
	shellCompletions[unknownShellName] = func(c *Command, appName string) (string, error) {
		if enableError {
			return "", fmt.Errorf("can't do completion")
		}
		return "something", nil
	}
	// buildCompletionCommand only turns shells listed in completionShells into
	// subcommands, so register the injected shell there too (restoring the
	// original slice afterward) for it to be reachable.
	defer func(orig []string) { completionShells = orig }(completionShells)
	completionShells = append(completionShells, unknownShellName)
	defer func() {
		delete(shellCompletions, unknownShellName)
	}()

	cmd := &Command{
		EnableShellCompletion: true,
	}

	err := cmd.Run(buildTestContext(t), []string{"foo", completionCommandName, unknownShellName})
	assert.ErrorContains(t, err, "can't do completion")
}

type mockWriter struct {
	err error
}

func (mw *mockWriter) Write(p []byte) (int, error) {
	if mw.err != nil {
		return 0, mw.err
	}
	return len(p), nil
}

func TestCompletionShellWriteError(t *testing.T) {
	shellName := "mock-shell"
	shellCompletions[shellName] = func(c *Command, appName string) (string, error) {
		return "something", nil
	}
	// buildCompletionCommand only turns shells listed in completionShells into
	// subcommands, so register the injected shell there too (restoring the
	// original slice afterward) for it to be reachable.
	defer func(orig []string) { completionShells = orig }(completionShells)
	completionShells = append(completionShells, shellName)
	defer func() {
		delete(shellCompletions, shellName)
	}()

	cmd := &Command{
		EnableShellCompletion: true,
		Writer:                &mockWriter{err: fmt.Errorf("writer error")},
	}

	err := cmd.Run(buildTestContext(t), []string{"foo", completionCommandName, shellName})
	assert.ErrorContains(t, err, "writer error")
}
