package envvars

import (
	breverrors "github.com/brevdev/brev-cli/pkg/errors"
	"github.com/brevdev/brev-cli/pkg/terminal"
	"github.com/spf13/cobra"
)

type EnvVarsStore interface{}

func NewCmdEnvVars(t *terminal.Terminal, evStore EnvVarsStore) *cobra.Command {
	cmd := &cobra.Command{
		Annotations:           map[string]string{"housekeeping": ""},
		Use:                   "configure-env-vars",
		DisableFlagsInUseLine: true,
		Short:                 "configure env vars in supported shells",
		Long:                  "Import your IDE config",
		Example:               "",
		RunE: func(cmd *cobra.Command, args []string) error {
			err := RunEnvVars(evStore)
			if err != nil {
				return breverrors.WrapAndTrace(err)
			}
			return nil
		},
	}

	return cmd
}

func RunEnvVars(evStore EnvVarsStore) error {
	return nil
}
