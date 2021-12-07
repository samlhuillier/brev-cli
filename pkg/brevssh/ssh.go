// Package brevssh exists to provide an api to configure and read from
// an ssh file
//
// brev ssh host file entry format:
//
// 	Host <workspace-dns-name>
// 		Hostname 0.0.0.0
// 		IdentityFile /home//.brev/brev.pem
//		User brev
//		Port <some-available-port>
//
// also think that file stuff should probably live in files package
// TODO migrate to using dns name for hostname
package brevssh

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"text/template"

	"github.com/brevdev/brev-cli/pkg/entity"
	breverrors "github.com/brevdev/brev-cli/pkg/errors"
	"github.com/kevinburke/ssh_config"
)

const workspaceSSHConfigTemplate = `Host {{ .Host }}
  Hostname {{ .Hostname }}
  IdentityFile {{ .IdentityFile }}
  User brev
  Port {{ .Port }}

`

type (
	BrevPorts          map[string]bool
	BrevHostValuesSet  map[string]bool
	IdentityPortMap    map[string]string
	workspaceSSHConfig struct {
		Host         string
		Hostname     string
		User         string
		IdentityFile string
		Port         string
	}
	SSHStore interface {
		GetSSHConfig() (string, error)
		WriteSSHConfig(config string) error
		CreateNewSSHConfigBackup() error
		WritePrivateKey(pem string) error
		GetPrivateKeyFilePath() string
	}
	Reader interface {
		GetBrevPorts() (BrevPorts, error)
		GetBrevHostValueSet() BrevHostValuesSet
	}
	Writer interface {
		Sync(identityPortMap IdentityPortMap) error
	}
	SSHConfig struct {
		store      SSHStore
		sshConfig  *ssh_config.Config
		privateKey string
	}
	SSHConfigurer struct {
		Reader
		Writer
		Writers    []Writer
		workspaces []entity.WorkspaceWithMeta
	}
	DefaultSSHConfigurer struct {
		workspaces []entity.WorkspaceWithMeta
		sshStore   SSHStore
		sshConfig  *ssh_config.Config
		privateKey string
	}
)

func hostnameFromString(hoststring string) string {
	hoststring = strings.TrimSpace(hoststring)
	if hoststring == "" {
		return ""
	}

	newLineSplit := strings.Split(hoststring, "\n")
	if len(newLineSplit) < 1 {
		return ""
	}
	spaceSplit := strings.Split(newLineSplit[0], " ")
	if len(spaceSplit) < 2 {
		return ""
	}

	return spaceSplit[1]
}

func checkIfBrevHost(host ssh_config.Host, privateKeyPath string) bool {
	for _, node := range host.Nodes {
		switch n := node.(type) { //nolint:gocritic // ignoring since want to keep options open for many cases
		case *ssh_config.KV:
			if strings.Compare(n.Key, "IdentityFile") == 0 {
				if strings.Compare(privateKeyPath, n.Value) == 0 {
					return true
				}
			}
		}
	}
	return false
}

func checkIfHostIsActive(hoststring string, activeWorksSpaces []string) bool {
	maybeHostname := hostnameFromString(hoststring)
	for _, name := range activeWorksSpaces {
		if name == maybeHostname {
			return true
		}
	}
	return false
}

// if a host is not a brev entry, it should stay in the config and there
// is nothing for us to do to it.
// if the host is a brev entry, make sure that it's hostname maps to an
// active workspace, otherwise this host should be deleted.
func createConfigEntry(hoststring string, isBrevHost, isActiveHost bool) string {
	if !isBrevHost {
		return hoststring
	}
	if isBrevHost && isActiveHost {
		return hoststring
	}
	return ""
}

func sshConfigFromString(config string) (*ssh_config.Config, error) {
	sshConfig, err := ssh_config.Decode(strings.NewReader(config))
	if err != nil {
		return nil, breverrors.WrapAndTrace(err)
	}
	return sshConfig, nil
}

func MakeSSHEntry(workspaceName, port, privateKeyPath string) (string, error) {
	wsc := workspaceSSHConfig{
		Host:         workspaceName,
		Hostname:     "0.0.0.0",
		User:         "brev",
		IdentityFile: privateKeyPath,
		Port:         port,
	}

	tmpl, err := template.New(workspaceName).Parse(workspaceSSHConfigTemplate)
	if err != nil {
		return "", breverrors.WrapAndTrace(err)
	}
	buf := &bytes.Buffer{}
	err = tmpl.Execute(buf, wsc)
	if err != nil {
		return "", breverrors.WrapAndTrace(err)
	}

	return buf.String(), nil
}

func NewDefaultSSHConfigurer(workspaces []entity.WorkspaceWithMeta, sshStore SSHStore, privateKey string) (*DefaultSSHConfigurer, error) {
	d := &DefaultSSHConfigurer{
		workspaces: workspaces,
		sshStore:   sshStore,
		privateKey: privateKey,
	}
	err := d.Init()
	if err != nil {
		return d, breverrors.WrapAndTrace(err)
	}
	return d, nil
}

func (s *DefaultSSHConfigurer) Init() error {
	configStr, err := s.sshStore.GetSSHConfig()
	if err != nil {
		return breverrors.WrapAndTrace(err)
	}

	s.sshConfig, err = ssh_config.Decode(strings.NewReader(configStr))
	if err != nil {
		return breverrors.WrapAndTrace(err)
	}
	return nil
}

func (s *DefaultSSHConfigurer) GetActiveWorkspaceIdentifiers() []string {
	var workspaceDNSNames []string
	for _, workspace := range s.workspaces {
		workspaceDNSNames = append(workspaceDNSNames, workspace.DNS)
	}
	return workspaceDNSNames
}

// ConfigureSSH
// inject active org id
// maybe just inject workspaces
// use project name instead of dns

// 	[x] 0. writes private key to disk
// 	[x] 1. gets a list of the current user's workspaces
// 	[x] 2. finds the user's ssh config file,
// 	[x] 3. looks at entries in the ssh config file and:
//         for each active workspace from brev delpoy
//            create ssh config entry if it does not exist
// 	[x] 4. After creating the ssh config entries, prune entries from workspaces
//        that exist in the ssh config but not as active workspaces.
// 	[ ] 5. Check for and remove duplicates?
// 	[x] 6. truncate old config and write new config back to disk (making backup of original copy first)
func (s *DefaultSSHConfigurer) Config() error {
	err := s.sshStore.WritePrivateKey(s.privateKey)
	if err != nil {
		return breverrors.WrapAndTrace(err)
	}

	// before doing potentially destructive work, backup the config
	err = s.sshStore.CreateNewSSHConfigBackup()
	if err != nil {
		return breverrors.WrapAndTrace(err)
	}

	err = s.CreateBrevSSHConfigEntries()
	if err != nil {
		return breverrors.WrapAndTrace(err)
	}

	err = s.PruneInactiveWorkspaces()
	if err != nil {
		return breverrors.WrapAndTrace(err)
	}

	err = s.sshStore.WriteSSHConfig(s.sshConfig.String())
	if err != nil {
		return breverrors.WrapAndTrace(err)
	}

	return nil
}

func (s DefaultSSHConfigurer) GetConfiguredWorkspacePort(workspace entity.Workspace) (string, error) {
	port, err := s.sshConfig.Get(workspace.DNS, "Port")
	if err != nil {
		return "", breverrors.WrapAndTrace(err)
	}
	return port, nil
}

func (s *DefaultSSHConfigurer) PruneInactiveWorkspaces() error {
	newConfig := ""

	privateKeyPath := s.sshStore.GetPrivateKeyFilePath()
	activeWorksSpaces := s.GetActiveWorkspaceIdentifiers()

	for _, host := range s.sshConfig.Hosts {
		hoststring := host.String()
		isBrevHost := checkIfBrevHost(*host, privateKeyPath)
		isActiveHost := checkIfHostIsActive(hoststring, activeWorksSpaces)
		newConfig += createConfigEntry(hoststring, isBrevHost, isActiveHost)
	}

	sshConfig, err := sshConfigFromString(newConfig)
	if err != nil {
		return breverrors.WrapAndTrace(err)
	}
	s.sshConfig = sshConfig
	return nil
}

func (s *DefaultSSHConfigurer) CreateBrevSSHConfigEntries() error {
	brevHostValues := s.GetBrevHostValues()
	brevHostValuesSet := make(map[string]bool)
	for _, hostValue := range brevHostValues {
		brevHostValuesSet[hostValue] = true
	}

	sshConfigStr := s.sshConfig.String()

	ports, err := s.GetBrevPorts(brevHostValues)
	if err != nil {
		return breverrors.WrapAndTrace(err)
	}
	port := 2222

	identifierPortMapping := make(map[string]string)
	for _, workspaceIdentifier := range s.GetActiveWorkspaceIdentifiers() {
		if !brevHostValuesSet[workspaceIdentifier] {
			for ports[fmt.Sprint(port)] {
				port++
			}
			identifierPortMapping[workspaceIdentifier] = strconv.Itoa(port)
			entry, err2 := s.makeSSHEntry(workspaceIdentifier, fmt.Sprint(port))
			if err2 != nil {
				return breverrors.WrapAndTrace(err2)
			}
			sshConfigStr += entry
			ports[fmt.Sprint(port)] = true
		}
	}
	s.sshConfig, err = ssh_config.Decode(strings.NewReader(sshConfigStr))
	if err != nil {
		return breverrors.WrapAndTrace(err)
	}
	return nil
}

func (s DefaultSSHConfigurer) GetBrevPorts(hostnames []string) (map[string]bool, error) {
	portSet := make(map[string]bool)

	for _, name := range hostnames {
		port, err := s.sshConfig.Get(name, "Port")
		if err != nil {
			return nil, breverrors.WrapAndTrace(err)
		}
		portSet[port] = true
	}
	return portSet, nil
}

// Hostname is a loaded term so using values
func (s DefaultSSHConfigurer) GetBrevHostValues() []string {
	privateKeyPath := s.sshStore.GetPrivateKeyFilePath()
	var brevHosts []string
	for _, host := range s.sshConfig.Hosts {
		hostname := hostnameFromString(host.String())
		// is this host a brev entry? if not, we don't care, and on to the
		// next one
		if checkIfBrevHost(*host, privateKeyPath) {
			brevHosts = append(brevHosts, hostname)
		}
	}
	return brevHosts
}

func (s DefaultSSHConfigurer) makeSSHEntry(workspaceName, port string) (string, error) {
	wsc := workspaceSSHConfig{
		Host:         workspaceName,
		Hostname:     "0.0.0.0",
		User:         "brev",
		IdentityFile: s.sshStore.GetPrivateKeyFilePath(),
		Port:         port,
	}

	tmpl, err := template.New(workspaceName).Parse(workspaceSSHConfigTemplate)
	if err != nil {
		return "", breverrors.WrapAndTrace(err)
	}
	buf := &bytes.Buffer{}
	err = tmpl.Execute(buf, wsc)
	if err != nil {
		return "", breverrors.WrapAndTrace(err)
	}

	return buf.String(), nil
}

func NewSSHConfig(store SSHStore) (*SSHConfig, error) {
	configStr, err := store.GetSSHConfig()
	if err != nil {
		return nil, breverrors.WrapAndTrace(err)
	}

	sshConfig, err := ssh_config.Decode(strings.NewReader(configStr))
	if err != nil {
		return nil, breverrors.WrapAndTrace(err)
	}
	return &SSHConfig{
		store:      store,
		sshConfig:  sshConfig,
		privateKey: store.GetPrivateKeyFilePath(),
	}, nil
}

func (s *SSHConfig) PruneInactiveWorkspaces(activeWorkspaces []string) error {
	newConfig := ""

	privateKeyPath := s.store.GetPrivateKeyFilePath()
	for _, host := range s.sshConfig.Hosts {
		hoststring := host.String()
		isBrevHost := checkIfBrevHost(*host, privateKeyPath)
		isActiveHost := checkIfHostIsActive(hoststring, activeWorkspaces)
		newConfig += createConfigEntry(hoststring, isBrevHost, isActiveHost)
	}

	sshConfig, err := sshConfigFromString(newConfig)
	if err != nil {
		return breverrors.WrapAndTrace(err)
	}
	s.sshConfig = sshConfig
	return nil
}

// Hostname is a loaded term so using values
func (s SSHConfig) GetBrevHostValues() []string {
	privateKeyPath := s.store.GetPrivateKeyFilePath()
	var brevHosts []string
	for _, host := range s.sshConfig.Hosts {
		hostname := hostnameFromString(host.String())
		// is this host a brev entry? if not, we don't care, and on to the
		// next one
		if checkIfBrevHost(*host, privateKeyPath) {
			brevHosts = append(brevHosts, hostname)
		}
	}
	return brevHosts
}

func (s *SSHConfig) Sync(identityPortMap IdentityPortMap) error {
	sshConfigString := s.sshConfig.String()
	var activeWorkspaces []string
	for key, value := range identityPortMap {
		entry, err := MakeSSHEntry(key, value, s.store.GetPrivateKeyFilePath())
		if err != nil {
			return breverrors.WrapAndTrace(err)
		}
		sshConfigString += entry
		activeWorkspaces = append(activeWorkspaces, key)

	}
	var err error
	s.sshConfig, err = ssh_config.Decode(strings.NewReader(sshConfigString))
	if err != nil {
		return breverrors.WrapAndTrace(err)
	}

	err = s.PruneInactiveWorkspaces(activeWorkspaces)
	if err != nil {
		return breverrors.WrapAndTrace(err)
	}
	err = s.store.WriteSSHConfig(s.sshConfig.String())
	if err != nil {
		return breverrors.WrapAndTrace(err)
	}

	return nil
}

func (s SSHConfig) GetBrevPorts() (BrevPorts, error) {
	var portSet BrevPorts
	hostnames := s.GetBrevHostValues()
	for _, name := range hostnames {
		port, err := s.sshConfig.Get(name, "Port")
		if err != nil {
			return nil, breverrors.WrapAndTrace(err)
		}
		portSet[port] = true
	}
	return portSet, nil
}

func (s SSHConfig) GetBrevHostValueSet() BrevHostValuesSet {
	var brevHostValuesSet BrevHostValuesSet
	brevHostValues := s.GetBrevHostValues()
	for _, hostValue := range brevHostValues {
		brevHostValuesSet[hostValue] = true
	}
	return brevHostValuesSet
}

func NewSSHConfigurer(workspaces []entity.WorkspaceWithMeta, reader Reader, writer Writer, writers []Writer) *SSHConfigurer {
	return &SSHConfigurer{
		workspaces: workspaces,
		Reader:     reader,
		Writer:     writer,
		Writers:    writers,
	}
}

func (sshConfigurer *SSHConfigurer) Sync() error {
	identityPortMap, err := sshConfigurer.GetIdentityPortMap()
	if err != nil {
		return breverrors.WrapAndTrace(err)
	}
	for _, writer := range sshConfigurer.Writers {
		err := writer.Sync(*identityPortMap)
		if err != nil {
			return breverrors.WrapAndTrace(err)
		}

	}

	return nil
}

func (sshConfigurer *SSHConfigurer) GetActiveWorkspaceIdentifiers() []string {
	var workspaceDNSNames []string
	for _, workspace := range sshConfigurer.workspaces {
		workspaceDNSNames = append(workspaceDNSNames, workspace.DNS)
	}
	return workspaceDNSNames
}

func (sshConfigurer SSHConfigurer) GetIdentityPortMap() (*IdentityPortMap, error) {
	var identifierPortMapping IdentityPortMap
	brevHostValuesSet := sshConfigurer.Reader.GetBrevHostValueSet()
	ports, err := sshConfigurer.Reader.GetBrevPorts()
	if err != nil {
		return nil, breverrors.WrapAndTrace(err)
	}

	port := 2222
	for _, workspaceIdentifier := range sshConfigurer.GetActiveWorkspaceIdentifiers() {
		if !brevHostValuesSet[workspaceIdentifier] {
			for ports[fmt.Sprint(port)] {
				port++
			}
			identifierPortMapping[workspaceIdentifier] = strconv.Itoa(port)
			ports[fmt.Sprint(port)] = true
		}
	}
	return &identifierPortMapping, nil
}
