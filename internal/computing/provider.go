package computing

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/filswan/go-mcs-sdk/mcs/api/common/logs"
	"github.com/mattn/go-isatty"
	"github.com/swanchain/computing-provider-v2/internal/setup"
)

func GetNodeId(cpRepoPath string) string {
	nodeID, _, _ := GenerateNodeID(cpRepoPath)
	return nodeID
}

func GenerateNodeID(cpRepoPath string) (string, string, string) {
	privateKeyPath := filepath.Join(cpRepoPath, "private_key")
	var privateKeyBytes []byte

	if _, err := os.Stat(privateKeyPath); err == nil {
		privateKeyBytes, err = os.ReadFile(privateKeyPath)
		if err != nil {
			log.Fatalf("Error reading private key: %v", err)
		}
	} else {
		privateKeyBytes = make([]byte, 32)
		_, err := rand.Read(privateKeyBytes)
		if err != nil {
			log.Fatalf("Error generating random key: %v", err)
		}

		err = os.MkdirAll(filepath.Dir(privateKeyPath), os.ModePerm)
		if err != nil {
			log.Fatalf("Error creating directory for private key: %v", err)
		}

		err = os.WriteFile(privateKeyPath, privateKeyBytes, 0644)
		if err != nil {
			log.Fatalf("Error writing private key: %v", err)
		}
	}

	privateKey, err := crypto.ToECDSA(privateKeyBytes)
	if err != nil {
		log.Fatalf("Error converting private key bytes: %v", err)
	}
	nodeID := hex.EncodeToString(crypto.FromECDSAPub(&privateKey.PublicKey))
	peerID := hashPublicKey(&privateKey.PublicKey)
	address := crypto.PubkeyToAddress(privateKey.PublicKey).String()
	return nodeID, peerID, address
}

func hashPublicKey(publicKey *ecdsa.PublicKey) string {
	publicKeyBytes := crypto.FromECDSAPub(publicKey)
	hash := sha256.Sum256(publicKeyBytes)
	return hex.EncodeToString(hash[:])
}

// fingerprintVersion prefixes stored fingerprints so the algorithm can evolve.
// Stored values without a version prefix are legacy (hostname + all MACs) and
// are migrated in place rather than treated as a mismatch — the legacy scheme
// included Docker veth/bridge MACs, so any container churn changed it.
const fingerprintVersion = "v2:"

// getMachineFingerprint generates a stable fingerprint for this machine.
// This is used to detect when a private_key has been copied to a different machine.
// Prefers the OS machine-id (survives interface churn); falls back to
// hostname + physical NIC MACs (virtual interfaces like Docker veths are
// excluded on Linux via /sys/class/net/<if>/device).
func getMachineFingerprint() string {
	if id := readMachineID(); id != "" {
		hash := sha256.Sum256([]byte("machine-id|" + id))
		return fingerprintVersion + hex.EncodeToString(hash[:16])
	}

	hostname, _ := os.Hostname()

	// Collect MAC addresses of physical interfaces only
	var macs []string
	interfaces, err := net.Interfaces()
	if err == nil {
		for _, iface := range interfaces {
			if iface.Flags&net.FlagLoopback != 0 || len(iface.HardwareAddr) == 0 {
				continue
			}
			if !isPhysicalInterface(iface.Name) {
				continue
			}
			macs = append(macs, iface.HardwareAddr.String())
		}
	}
	sort.Strings(macs)

	raw := fmt.Sprintf("%s|%s", hostname, strings.Join(macs, ","))
	hash := sha256.Sum256([]byte(raw))
	return fingerprintVersion + hex.EncodeToString(hash[:16]) // 16 bytes = 32 hex chars
}

// readMachineID returns the systemd machine-id if available (Linux).
func readMachineID() string {
	for _, p := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
		if data, err := os.ReadFile(p); err == nil {
			if id := strings.TrimSpace(string(data)); id != "" {
				return id
			}
		}
	}
	return ""
}

// isPhysicalInterface reports whether the named interface is backed by real
// hardware. On Linux, physical NICs have a /sys/class/net/<if>/device link;
// virtual interfaces (veth, docker0, bridges, tunnels) do not. On other
// platforms every interface is treated as physical.
func isPhysicalInterface(name string) bool {
	if _, err := os.Stat("/sys/class/net"); err != nil {
		return true // non-Linux: no way to tell, keep legacy behavior
	}
	_, err := os.Stat(filepath.Join("/sys/class/net", name, "device"))
	return err == nil
}

// CheckMachineIdentity verifies that the private_key belongs to this machine.
// If the key was generated on a different machine (copied config), it prompts the user
// to regenerate a new node-id. In non-interactive environments, it returns an error.
func CheckMachineIdentity(cpRepoPath string) error {
	fingerprintPath := filepath.Join(cpRepoPath, "machine_fingerprint")
	currentFingerprint := getMachineFingerprint()

	stored, err := os.ReadFile(fingerprintPath)
	if err != nil {
		// First run or file missing — write fingerprint and continue
		_ = os.WriteFile(fingerprintPath, []byte(currentFingerprint), 0644)
		return nil
	}

	storedFingerprint := strings.TrimSpace(string(stored))
	if storedFingerprint == currentFingerprint {
		// Fingerprint matches — no issue
		_ = os.WriteFile(fingerprintPath, []byte(currentFingerprint), 0644)
		return nil
	}

	if !strings.HasPrefix(storedFingerprint, fingerprintVersion) {
		// Legacy fingerprint (pre-versioning, hostname + all MACs including
		// Docker veths) — unstable by design, so migrate rather than mismatch
		logs.GetLogger().Infof("Migrating machine fingerprint to %s format", strings.TrimSuffix(fingerprintVersion, ":"))
		_ = os.WriteFile(fingerprintPath, []byte(currentFingerprint), 0644)
		return nil
	}

	// === MISMATCH: private_key was copied from another machine ===
	logs.GetLogger().Warnf("WARNING: This private_key was generated on a different machine (fingerprint mismatch).")
	logs.GetLogger().Warnf("Each machine MUST have its own private_key to get a unique node-id.")
	logs.GetLogger().Warnf("Using the same private_key on multiple machines causes them to kick each other offline.")

	interactive := isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())
	if !interactive {
		return fmt.Errorf("machine fingerprint mismatch: this private_key was copied from another machine. "+
			"Delete '%s/private_key' and restart to generate a new node-id, or run interactively to be prompted",
			cpRepoPath)
	}

	prompter := setup.NewPrompter()
	regenerate, err := prompter.AskYesNo("Generate a new node-id for this machine?", true)
	if err != nil {
		return fmt.Errorf("failed to read user input: %w", err)
	}

	if regenerate {
		privateKeyPath := filepath.Join(cpRepoPath, "private_key")
		if err := os.Remove(privateKeyPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove old private_key: %w", err)
		}
		logs.GetLogger().Infof("Removed old private_key. A new node-id will be generated on startup.")
		// Write current machine fingerprint so the next startup won't trigger mismatch again
		_ = os.WriteFile(fingerprintPath, []byte(currentFingerprint), 0644)
		return nil
	}

	// User chose to keep existing key on this machine
	logs.GetLogger().Warnf("Continuing with existing private_key — both machines will share the same node-id.")
	_ = os.WriteFile(fingerprintPath, []byte(currentFingerprint), 0644)
	return nil
}
