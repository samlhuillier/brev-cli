package cmderrors

import (
	"github.com/pkg/errors"

	"github.com/brevdev/brev-cli/pkg/featureflag"

	breverrors "github.com/brevdev/brev-cli/pkg/errors"
)

// determines if should print error stack trace and/or send to crash monitor
func DisplayAndHandleCmdError(name string, cmdFunc func() error) error {
	er := breverrors.GetDefaultErrorReporter()
	er.AddTag("command", name)
	err := cmdFunc()
	if err != nil {
		er.ReportMessage(err.Error())
		er.ReportError(err)
		if featureflag.Debug() || featureflag.IsDev() {
			return err
		} else {
			return errors.Cause(err) //nolint:wrapcheck //no check
		}
	}
	return nil
}
