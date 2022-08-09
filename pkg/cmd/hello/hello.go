package hello

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/brevdev/brev-cli/pkg/entity"
	breverrors "github.com/brevdev/brev-cli/pkg/errors"
	"github.com/brevdev/brev-cli/pkg/store"
	"github.com/brevdev/brev-cli/pkg/terminal"

	"github.com/spf13/cobra"
)

type HelloStore interface {
	GetAllWorkspaces(options *store.GetWorkspacesOptions) ([]entity.Workspace, error)
	GetCurrentUser() (*entity.User, error)
	UpdateUser(userID string, updatedUser *entity.UpdateUser) (*entity.User, error)
}

func NewCmdHello(t *terminal.Terminal, store HelloStore) *cobra.Command {
	cmd := &cobra.Command{
		Annotations:           map[string]string{"housekeeping": ""},
		Use:                   "hello",
		DisableFlagsInUseLine: true,
		Long:                  "Get a quick onboarding of the Brev CLI",
		Short:                 "Get a quick onboarding of the Brev CLI",
		Example:               "brev hello",
		RunE: func(cmd *cobra.Command, args []string) error {
			// terminal.DisplayBrevLogo(t)
			t.Vprint("\n")

			user, err := store.GetCurrentUser()
			if err != nil {
				return breverrors.WrapAndTrace(err)
			}

			RunOnboarding(t, user, store)
			return nil
		},
	}

	return cmd
}

func TypeItToMe(s string) {
	sleepSpeed := 47

	// Make outgoing reader routine
	outgoing := make(chan string)
	go func() {
		inputReader := bufio.NewReader(os.Stdin)
		for {
			o, err := inputReader.ReadString('\n')
			if err != nil {
				fmt.Printf("outgoing error: %v", err)
				return
			}
			outgoing <- o
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer ctx.Done()
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		for {
			select {
			case <-outgoing:
				sleepSpeed = sleepSpeed / 2

			case <-interrupt:
				sleepSpeed = 0

			case <-ctx.Done():
				cancel()
			}
		}
	}()

	sRunes := []rune(s)
	for i := 0; i < len(sRunes); i++ {
		time.Sleep(time.Duration(sleepSpeed) * time.Millisecond)
		fmt.Printf("%c", sRunes[i])
	}
}

func TypeItToMeUnskippable(s string) {
	sRunes := []rune(s)
	for i := 0; i < len(sRunes); i++ {
		time.Sleep(37 * time.Millisecond)

		fmt.Printf("%c", sRunes[i])
	}
}

var wg sync.WaitGroup

func RunOnboarding(t *terminal.Terminal, user *entity.User, store HelloStore) {
	// Reset the onboarding object to walk through the onboarding fresh
	SetOnboardingObject(OnboardingObject{0, false, false})

	terminal.DisplayBrevLogo(t)
	t.Vprint("\n")

	s := "Hey " + GetFirstName(user.Name) + "!\n"

	s += "\n\nI'm Nader 👋  Co-founder of Brev. I'll show you around"
	s += "\nbtw, text me or call me if you need anything"
	s += ". My cell is " + t.Yellow("(415) 237-2247")

	s += "\n\nBrev is a dev tool for creating and sharing dev environments"
	s += "\nRun " + t.Green("brev ls") + " to see your dev environments 👇\n"

	wg.Add(2)
	go finishOutput(t, s)
	go MarkOnboardingStepCompleted(t, user, store)
	wg.Wait()
}

func finishOutput(t *terminal.Terminal, s string) {
	TypeItToMe(s)
	wg.Done()
}

func MarkOnboardingStepCompleted(t *terminal.Terminal, user *entity.User, store HelloStore) {
	CompletedOnboardingIntro(user, store)
	wg.Done()
}
