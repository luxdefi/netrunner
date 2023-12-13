package local

import (
	"encoding/base64"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"time"

	"github.com/luxdefi/netrunner/network/node"
	"github.com/luxdefi/node/config"
	"github.com/luxdefi/node/utils/constants"
	"github.com/luxdefi/node/utils/logging"
	"go.uber.org/zap"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

// writeFiles writes the files a node needs on startup.
// It returns flags used to point to those files.
func writeFiles(networkID uint32, genesis []byte, nodeRootDir string, nodeConfig *node.Config) (map[string]string, error) {
	type file struct {
		pathKey   string
		flagValue string
		path      string
		contents  []byte
	}
	decodedStakingSigningKey, err := base64.StdEncoding.DecodeString(nodeConfig.StakingSigningKey)
	if err != nil {
		return nil, err
	}
	files := []file{
		{
			flagValue: filepath.Join(nodeRootDir, stakingKeyFileName),
			path:      filepath.Join(nodeRootDir, stakingKeyFileName),
			pathKey:   config.StakingTLSKeyPathKey,
			contents:  []byte(nodeConfig.StakingKey),
		},
		{
			flagValue: filepath.Join(nodeRootDir, stakingCertFileName),
			path:      filepath.Join(nodeRootDir, stakingCertFileName),
			pathKey:   config.StakingCertPathKey,
			contents:  []byte(nodeConfig.StakingCert),
		},
		{
			flagValue: filepath.Join(nodeRootDir, stakingSigningKeyFileName),
			path:      filepath.Join(nodeRootDir, stakingSigningKeyFileName),
			pathKey:   config.StakingSignerKeyPathKey,
			contents:  decodedStakingSigningKey,
		},
	}
	if networkID != constants.LocalID {
		files = append(files, file{
			flagValue: filepath.Join(nodeRootDir, genesisFileName),
			path:      filepath.Join(nodeRootDir, genesisFileName),
			pathKey:   config.GenesisFileKey,
			contents:  genesis,
		})
	}
	if len(nodeConfig.ConfigFile) != 0 {
		files = append(files, file{
			flagValue: filepath.Join(nodeRootDir, configFileName),
			path:      filepath.Join(nodeRootDir, configFileName),
			pathKey:   config.ConfigFileKey,
			contents:  []byte(nodeConfig.ConfigFile),
		})
	}
	flags := map[string]string{}
	for _, f := range files {
		flags[f.pathKey] = f.flagValue
		if err := createFileAndWrite(f.path, f.contents); err != nil {
			return nil, fmt.Errorf("couldn't write file at %q: %w", f.path, err)
		}
	}
	// chain configs dir
	chainConfigDir := filepath.Join(nodeRootDir, chainConfigSubDir)
	if err := os.MkdirAll(chainConfigDir, 0o750); err != nil {
		return nil, err
	}
	flags[config.ChainConfigDirKey] = chainConfigDir
	// subnet configs dir
	subnetConfigDir := filepath.Join(nodeRootDir, subnetConfigSubDir)
	if err := os.MkdirAll(subnetConfigDir, 0o750); err != nil {
		return nil, err
	}
	flags[config.SubnetConfigDirKey] = subnetConfigDir
	// chain configs
	for chainAlias, chainConfigFile := range nodeConfig.ChainConfigFiles {
		chainConfigPath := filepath.Join(chainConfigDir, chainAlias, configFileName)
		if err := createFileAndWrite(chainConfigPath, []byte(chainConfigFile)); err != nil {
			return nil, fmt.Errorf("couldn't write file at %q: %w", chainConfigPath, err)
		}
	}
	// network upgrades
	for chainAlias, chainUpgradeFile := range nodeConfig.UpgradeConfigFiles {
		chainUpgradePath := filepath.Join(chainConfigDir, chainAlias, upgradeConfigFileName)
		if err := createFileAndWrite(chainUpgradePath, []byte(chainUpgradeFile)); err != nil {
			return nil, fmt.Errorf("couldn't write file at %q: %w", chainUpgradePath, err)
		}
	}
	// subnet configs
	for subnetID, subnetConfigFile := range nodeConfig.SubnetConfigFiles {
		subnetConfigPath := filepath.Join(subnetConfigDir, subnetID+".json")
		if err := createFileAndWrite(subnetConfigPath, []byte(subnetConfigFile)); err != nil {
			return nil, fmt.Errorf("couldn't write file at %q: %w", subnetConfigPath, err)
		}
	}
	return flags, nil
}

// getConfigEntry returns an entry in the config file if it is found, otherwise returns the default value
func getConfigEntry(
	nodeConfigFlags map[string]interface{},
	configFile map[string]interface{},
	flag string,
	defaultVal string,
) (string, error) {
	var entry string
	if val, ok := nodeConfigFlags[flag]; ok {
		if entry, ok := val.(string); ok {
			return entry, nil
		}
		return "", fmt.Errorf("expected node config flag %q to be string but got %T", flag, entry)
	}
	if val, ok := configFile[flag]; ok {
		if entry, ok := val.(string); ok {
			return entry, nil
		}
		return "", fmt.Errorf("expected config file flag %q to be string but got %T", flag, entry)
	}
	return defaultVal, nil
}

// getPort looks up the port config in the config file, if there is none, it tries to get a random free port from the OS
// if [reassingIfUsed] is true, and the port from config is not free, also tries to get a random free port
func getPort(
	flags map[string]interface{},
	configFile map[string]interface{},
	portKey string,
) (port uint16, err error) {
	if portIntf, ok := flags[portKey]; ok {
		switch gotPort := portIntf.(type) {
		case int:
			port = uint16(gotPort)
		case float64:
			port = uint16(gotPort)
		default:
			return 0, fmt.Errorf("expected flag %q to be int/float64 but got %T", portKey, portIntf)
		}
	} else if portIntf, ok := configFile[portKey]; ok {
		portFromConfigFile, ok := portIntf.(float64)
		if !ok {
			return 0, fmt.Errorf("expected flag %q to be float64 but got %T", portKey, portIntf)
		}
		port = uint16(portFromConfigFile)
	} else {
		port, err = getFreePort()
		if err != nil {
			return 0, fmt.Errorf("couldn't get free port: %w", err)
		}
	}
	return port, nil
}

func makeNodeDir(log logging.Logger, rootDir, nodeName string) (string, error) {
	if rootDir == "" {
		log.Warn("no network root directory defined; will create this node's runtime directory in working directory")
	}
	// [nodeRootDir] is where this node's config file, C-Chain config file,
	// staking key, staking certificate and genesis file will be written.
	// (Other file locations are given in the node's config file.)
	// TODO should we do this for other directories? Profiles?
	nodeRootDir := getNodeDir(rootDir, nodeName)
	if err := os.Mkdir(nodeRootDir, 0o755); err != nil {
		if !os.IsExist(err) {
			return "", fmt.Errorf("error creating temp dir %w", err)
		}
		log.Warn("node root directory already exists", zap.String("root-dir", nodeRootDir))
	}
	return nodeRootDir, nil
}

func getNodeDir(rootDir string, nodeName string) string {
	return filepath.Join(rootDir, nodeName)
}

// createFileAndWrite creates a file with the given path and
// writes the given contents
func createFileAndWrite(path string, contents []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(contents)
	return err
}

// addNetworkFlags adds the flags in [networkFlags] to [nodeConfig.Flags].
// [nodeFlags] must not be nil.
func addNetworkFlags(networkFlags map[string]interface{}, nodeFlags map[string]interface{}) {
	for flagName, flagVal := range networkFlags {
		// If the same flag is given in network config and node config,
		// the flag in the node config takes precedence
		if _, ok := nodeFlags[flagName]; !ok {
			nodeFlags[flagName] = flagVal
		}
	}
}
