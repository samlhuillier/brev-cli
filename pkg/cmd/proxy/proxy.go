package proxy

import (
	"fmt"
	"strings"

	"github.com/brevdev/brev-cli/pkg/cmd/cmderrors"
	"github.com/brevdev/brev-cli/pkg/entity"
	breverrors "github.com/brevdev/brev-cli/pkg/errors"
	"github.com/brevdev/brev-cli/pkg/featureflag"
	"github.com/brevdev/brev-cli/pkg/huproxyclient"
	"github.com/brevdev/brev-cli/pkg/terminal"
	"github.com/hashicorp/go-version"
	"github.com/spf13/cobra"
)

const (
	allowedWorkspaceImage        = "brevdev/ubuntu-proxy"
	allowedWorkspaceImageTag     = ">= 0.3"
	allowedWorkspaceInfraVersion = ">= 1.7"
)

type ProxyStore interface {
	huproxyclient.HubProxyStore
	GetWorkspace(workspaceID string) (*entity.Workspace, error)
	WritePrivateKey(pem string) error
	GetCurrentUserKeys() (*entity.UserKeys, error)
}

func NewCmdProxy(t *terminal.Terminal, store ProxyStore) *cobra.Command {
	cmd := &cobra.Command{
		Annotations:           map[string]string{"hidden": ""},
		Use:                   "proxy",
		DisableFlagsInUseLine: true,
		Short:                 "http upgrade proxy",
		Long:                  "http upgrade proxy for ssh ProxyCommand directive to use",
		Args:                  cmderrors.TransformToValidationError(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			err := Proxy(t, store, args[0])
			if err != nil {
				return breverrors.WrapAndTrace(err)
			}
			return nil
		},
	}

	return cmd
}

func Proxy(_ *terminal.Terminal, store ProxyStore, workspaceID string) error {
	workspace, err := store.GetWorkspace(workspaceID)
	if err != nil {
		return breverrors.WrapAndTrace(err)
	}

	err = CheckWorkspaceCanSSH(workspace)
	if err != nil {
		return breverrors.WrapAndTrace(err)
	}

	err = WriteUserPrivateKey(store)
	if err != nil {
		return breverrors.WrapAndTrace(err)
	}

	url := makeProxyURL(workspace)
	err = huproxyclient.Run(url, store)
	if err != nil {
		return breverrors.WrapAndTrace(err)
	}
	return nil
}

func makeProxyURL(w *entity.Workspace) string {
	return fmt.Sprintf("wss://%s/proxy", w.GetSSHURL())
}

func CheckWorkspaceCanSSH(workspace *entity.Workspace) error {
	if !featureflag.DisableSSHProxyVersionCheck() {
		fmt.Println("checking dev environment version")
		err := checkWorkspaceInfraVersionOrErr(workspace)
		if err != nil {
			return breverrors.WrapAndTrace(err)
		}
		fmt.Println("checking dev environment image version")
		err = checkWorkspaceImageVersionOrErr(workspace)
		if err != nil {
			return breverrors.WrapAndTrace(err)
		}
	}
	err := checkWorkspaceStatusOrErr(workspace)
	if err != nil {
		return breverrors.WrapAndTrace(err)
	}
	return nil
}

func checkWorkspaceStatusOrErr(workspace *entity.Workspace) error {
	if workspace.Status != entity.Running {
		return breverrors.NewValidationError(fmt.Sprintf("dev environment is not in RUNNING state, status: %s", workspace.Status))
	}
	return nil
}

func checkWorkspaceImageVersionOrErr(workspace *entity.Workspace) error {
	imageSplit := strings.Split(workspace.WorkspaceTemplate.Image, ":")
	if len(imageSplit) != 2 {
		return breverrors.NewValidationError("problem parsing workspace image tag")
	}
	wiv, err := version.NewVersion(imageSplit[1])
	if err != nil {
		if !strings.Contains(err.Error(), "Malformed") {
			return breverrors.WrapAndTrace(err)
		} else {
			_ = 0
			// skip checking constraints since probably a test image
		}
	} else {
		imageContraints, err := version.NewConstraint(allowedWorkspaceImageTag)
		if err != nil {
			return breverrors.WrapAndTrace(err)
		}

		if !imageContraints.Check(wiv) && !strings.HasSuffix(imageSplit[0], allowedWorkspaceImage) {
			return breverrors.NewValidationError(fmt.Sprintf("dev environment image version %s is not supported with this cli version\n upgrade your dev environment or downgrade your cli", workspace.WorkspaceTemplate.Image))
		}
	}
	return nil
}

func checkWorkspaceInfraVersionOrErr(workspace *entity.Workspace) error {
	fmt.Printf("dev environment version: %s\n", workspace.Version)
	if workspace.Version != "" {
		wv, err := version.NewVersion(workspace.Version)
		if err != nil {
			return breverrors.WrapAndTrace(err)
		}
		workspaceInfraConstraints, err := version.NewConstraint(allowedWorkspaceInfraVersion)
		if err != nil {
			return breverrors.WrapAndTrace(err)
		}
		if !workspaceInfraConstraints.Check(wv) {
			return breverrors.NewValidationError(fmt.Sprintf("dev environment of version %s is not supported with this cli version\n upgrade your dev environment or downgrade your cli. Supported %s", workspace.Version, allowedWorkspaceInfraVersion))
		}
	} else {
		fmt.Println("dev environment version blank assuming dev, not checking constraint")
	}
	return nil
}

func WriteUserPrivateKey(store ProxyStore) error {
	keys, err := store.GetCurrentUserKeys()
	if err != nil {
		return breverrors.WrapAndTrace(err)
	}
	err = store.WritePrivateKey(keys.PrivateKey)
	if err != nil {
		return breverrors.WrapAndTrace(err)
	}
	return nil
}
