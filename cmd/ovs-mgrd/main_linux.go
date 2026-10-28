//go:build linux

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/buildinfo"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"github.com/sampsonlor/ovs-webui/internal/ipc"
	linuxprovider "github.com/sampsonlor/ovs-webui/internal/provider/linux"
	ovsprovider "github.com/sampsonlor/ovs-webui/internal/provider/ovsdb"
	"github.com/sampsonlor/ovs-webui/internal/redact"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/repository/auth"
	"github.com/sampsonlor/ovs-webui/internal/repository/executions"
	registry "github.com/sampsonlor/ovs-webui/internal/repository/inventory"
	"github.com/sampsonlor/ovs-webui/internal/repository/secrets"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
	"github.com/sampsonlor/ovs-webui/internal/runtimehost"
	"github.com/sampsonlor/ovs-webui/internal/safety"
	"github.com/sampsonlor/ovs-webui/internal/secret"
)

func main() { os.Exit(run()) }
func run() int {
	socket := flag.String("socket", "/run/ovs-webui/mgrd.sock", "Root-owned Unix socket path")
	uid := flag.Uint("webd-uid", 0, "Required fixed non-root ovs-webd UID")
	gid := flag.Uint("webd-gid", 0, "Required ovs-webd socket group GID")
	version := flag.Bool("version", false, "Print build version")
	database := flag.String("database", "/var/lib/ovs-webui/manager/manager.db", "Private manager database path")
	initialize := flag.Bool("init-database", false, "Explicitly initialize a new database and exit; never overwrite")
	authKeyPath := flag.String("auth-key-file", "", "Private authentication key (default: auth.key beside manager.db)")
	initAuthKey := flag.Bool("init-auth-key", false, "Explicitly create a new authentication key and exit; never overwrite")
	bootstrap := flag.String("bootstrap-admin", "", "Initialize first local administrator; read password from stdin, then exit")
	grantTreeRole := flag.String("grant-spanning-tree-execution-role", "", "Offline root action: explicitly grant one existing role the independent STP/RSTP write capability, then exit; new login required")
	grantIsolationRole := flag.String("grant-port-isolation-role", "", "Offline root action: grant one existing role native Port isolation capability, then exit; new login required")
	revokeIsolationRole := flag.String("revoke-port-isolation-role", "", "Offline root action: revoke one role native Port isolation capability, then exit")
	grantMulticastRole := flag.String("grant-multicast-role", "", "Offline root action: explicitly grant one existing role native multicast capability, then exit; new login required")
	revokeMulticastRole := flag.String("revoke-multicast-role", "", "Offline root action: revoke one role native multicast capability, then exit")
	grantMirrorRole := flag.String("grant-mirror-role", "", "Offline root action: grant one existing role native Mirror capability, then exit; new login required")
	revokeMirrorRole := flag.String("revoke-mirror-role", "", "Offline root action: revoke one role native Mirror capability, then exit")
	grantTunnelRole := flag.String("grant-static-tunnel-role", "", "Offline root action: grant one existing role native static Tunnel capability, then exit; new login required")
	revokeTunnelRole := flag.String("revoke-static-tunnel-role", "", "Offline root action: revoke one role native static Tunnel capability, then exit")
	revokeTreeRole := flag.String("revoke-spanning-tree-execution-role", "", "Offline root action: revoke one role's independent STP/RSTP write capability, then exit")
	secretDir := flag.String("secret-key-directory", "", "Private versioned privileged keys (default: secret-keys beside manager.db)")
	initSecret := flag.Bool("init-secret-store", false, "Explicitly initialize privileged SecretStore keys and exit")
	rotateSecret := flag.Bool("rotate-secret-key", false, "Offline privileged key rotation, retaining old versions")
	restore := flag.Bool("prepare-restore-security", false, "Offline: rotate restored auth/request epochs and revoke all grants/tokens; run with webd reconciliation")
	ovsSocket := flag.String("ovsdb-socket", "/run/openvswitch/db.sock", "Local OVSDB Unix socket for typed inventory and guarded execution")
	ovsFile := flag.String("ovsdb-file", "/var/lib/openvswitch/conf.db", "Canonical database file for lifecycle evidence (no symlinks)")
	ovsUID := flag.Uint("ovsdb-peer-uid", 0, "Required OVSDB Unix peer UID")
	acceptEvidence := flag.String("reconcile-ovsdb", "", "Offline: accept an exact reviewed inventory evidence digest, assign a new generation, then exit")
	acceptReason := flag.String("reconciliation-reason", "", "Administrative reason for offline identity reconciliation")
	localSpanningTree := flag.String("local-spanning-tree-bridges", "", "Reviewed immutable Bridge IDs permitted to prepare STP/RSTP drafts; no execution grant, independent of topology and Port authority")
	localIsolation := flag.String("local-isolation-ports", "", "Independent reviewed immutable Port IDs permitted for native protected-Port Safe Apply")
	localMulticast := flag.String("local-multicast-bridges", "", "Independent reviewed immutable Bridge IDs permitted for native multicast Safe Apply")
	multicastControl := flag.String("multicast-group-witness-socket", "", "Optional explicit native multicast membership read source; no write authority")
	multicastExecutable := flag.String("multicast-group-witness-executable", "", "Root-owned qualified optional OVS witness executable")
	multicastManifest := flag.String("multicast-group-witness-manifest", "", "Root-owned exact optional OVS witness build manifest")
	multicastManifestDigest := flag.String("multicast-group-witness-manifest-sha256", "", "Reviewed digest of the exact optional OVS witness build manifest")
	localSpanningTreeExecution := flag.String("local-spanning-tree-execution-bridges", "", "Independent reviewed Bridge IDs permitted for guarded STP/RSTP Safe Apply; requires an explicit qualified witness build")
	treeControl := flag.String("spanning-tree-witness-socket", "", "Explicit local ovs-vswitchd Unix control socket for read-only installed witnesses")
	treeExecutable := flag.String("spanning-tree-witness-executable", "", "Root-owned executable of the qualified optional OVS witness build")
	treeManifest := flag.String("spanning-tree-witness-manifest", "", "Root-owned exact optional OVS build manifest")
	treeManifestDigest := flag.String("spanning-tree-witness-manifest-sha256", "", "Reviewed SHA256 of the exact optional OVS build manifest")
	treeDatabaseExecutable := flag.String("spanning-tree-database-executable", "", "Root-owned qualified standalone ovsdb-server executable")
	treeDatabaseSHA256 := flag.String("spanning-tree-database-sha256", "", "Reviewed SHA256 of the qualified standalone ovsdb-server executable")
	localTopology := flag.String("local-topology-objects", "", "Reviewed immutable graph object IDs with topology authority; every touched existing object requires a grant")
	localTopologyCreates := flag.String("local-topology-create-targets", "", "Reviewed Bridge management-id:new-name grants for each new Port and Interface")
	mirrorControl := flag.String("mirror-runtime-socket", "", "Explicit local ovs-vswitchd control socket for native Mirror build qualification")
	mirrorExecutable := flag.String("mirror-runtime-executable", "", "Root-owned qualified fixed Mirror ovs-vswitchd executable")
	mirrorManifest := flag.String("mirror-runtime-manifest", "", "Root-owned exact fixed Mirror build manifest")
	mirrorManifestDigest := flag.String("mirror-runtime-manifest-sha256", "", "Reviewed SHA256 of the exact fixed Mirror build manifest")
	localMirrorObjects := flag.String("local-mirror-objects", "", "Independent immutable graph IDs permitted for native Mirror changes")
	localMirrorCreates := flag.String("local-mirror-create-targets", "", "Independent Bridge management-id:new-name grants for native Mirror sessions")
	localTunnelObjects := flag.String("local-tunnel-objects", "", "Independent immutable graph IDs permitted for native static Tunnel changes")
	localTunnelCreates := flag.String("local-tunnel-create-targets", "", "Independent Bridge management-id:new-name grants for new native static Tunnel Port and Interface pairs")
	localVLANPorts := flag.String("local-vlan-ports", "", "Reviewed comma-separated Port management IDs with local VLAN authority; default unknown, no write access implied")
	localBondPorts := flag.String("local-bond-ports", "", "Reviewed comma-separated Port management IDs with local Bond/LACP authority; independent of VLAN authority")
	localMTUInterfaces := flag.String("local-mtu-interfaces", "", "Reviewed Interface management IDs with explicit internal MTU authority; independent of Port grants")
	localPolicingInterfaces := flag.String("local-policing-interfaces", "", "Reviewed internal Interface management IDs with exclusive Linux ingress management; independent of MTU/Port grants")
	localInternalPortDeleteTargets := flag.String("local-internal-port-delete-targets", "", "Reviewed Bridge management-id:managed-internal-port-name deletion grants; independent of creation authority")
	localInternalPortTargets := flag.String("local-internal-port-targets", "", "Reviewed existing Bridge management-id:new-internal-port-name pairs; independent of VLAN/Bond/Bridge authority")
	localBridgeNames := flag.String("local-bridge-create-names", "", "Reviewed names for NEW isolated system Bridges only; never adopts an existing object")
	localBridgeDeleteNames := flag.String("local-bridge-delete-names", "", "Reviewed names for deleting unchanged manager-created isolated Bridges; independent of creation authority")
	probeAddress := flag.String("safe-apply-probe-address", "", "Reviewed numeric management endpoint IP:TCP-port; empty disables Safe Apply")
	probeInterface := flag.String("safe-apply-probe-interface", "", "Reviewed management interface; probe sockets bind to this device")
	flag.Parse()
	if *version {
		fmt.Println(buildinfo.SoftwareVersion())
		return 0
	}
	logger := slog.New(redact.New(slog.NewJSONHandler(os.Stderr, nil))).With("service", "ovs-mgrd")
	multicastGroupsConfigured := *multicastControl != "" || *multicastExecutable != "" || *multicastManifest != "" || *multicastManifestDigest != ""
	if multicastGroupsConfigured && (*multicastControl == "" || *multicastExecutable == "" || *multicastManifest == "" || *multicastManifestDigest == "") {
		logger.Error("invalid_process_configuration", "code", "INCOMPLETE_MULTICAST_GROUP_SOURCE")
		return 2
	}
	treeConfigured := *localSpanningTreeExecution != "" || *treeControl != "" || *treeExecutable != "" || *treeManifest != "" || *treeManifestDigest != "" || *treeDatabaseExecutable != "" || *treeDatabaseSHA256 != ""
	if treeConfigured && (*localSpanningTreeExecution == "" || *treeControl == "" || *treeExecutable == "" || *treeManifest == "" || *treeManifestDigest == "" || *treeDatabaseExecutable == "" || *treeDatabaseSHA256 == "") {
		logger.Error("invalid_process_configuration", "code", "INCOMPLETE_SPANNING_TREE_WITNESS_CONFIGURATION")
		return 2
	}
	mirrorConfigured := *mirrorControl != "" || *mirrorExecutable != "" || *mirrorManifest != "" || *mirrorManifestDigest != ""
	if mirrorConfigured && (*mirrorControl == "" || *mirrorExecutable == "" || *mirrorManifest == "" || *mirrorManifestDigest == "") {
		logger.Error("invalid_process_configuration", "code", "INCOMPLETE_MIRROR_RUNTIME_CONFIGURATION")
		return 2
	}
	actions := 0
	if *grantMirrorRole != "" {
		actions++
	}
	if *revokeMirrorRole != "" {
		actions++
	}
	if *grantTunnelRole != "" {
		actions++
	}
	if *revokeTunnelRole != "" {
		actions++
	}
	if *grantMulticastRole != "" {
		actions++
	}
	if *revokeMulticastRole != "" {
		actions++
	}
	if *grantIsolationRole != "" {
		actions++
	}
	if *revokeIsolationRole != "" {
		actions++
	}
	for _, enabled := range []bool{*initialize, *initAuthKey, *bootstrap != "", *grantTreeRole != "", *revokeTreeRole != "", *initSecret, *rotateSecret, *restore, *acceptEvidence != ""} {
		if enabled {
			actions++
		}
	}
	if flag.NArg() != 0 || os.Geteuid() != 0 || actions > 1 || (actions == 0 && (*uid == 0 || *gid == 0 || uint64(*uid) >= 1<<32-1 || uint64(*gid) >= 1<<32-1)) {
		logger.Error("invalid_process_configuration", "code", "ROOT_AND_FIXED_WEBD_ID_REQUIRED")
		return 2
	}
	syscall.Umask(0077)
	if *authKeyPath == "" {
		*authKeyPath = filepath.Join(filepath.Dir(*database), "auth.key")
	}
	if *secretDir == "" {
		*secretDir = filepath.Join(filepath.Dir(*database), "secret-keys")
	}
	if *initAuthKey {
		if _, err := authn.KeyFile(*authKeyPath, true); err != nil {
			logger.Error("auth_key_initialization_failed", "code", "AUTH_KEY_UNAVAILABLE")
			return 1
		}
		logger.Info("auth_key_initialized")
		return 0
	}
	options := sqlite.Options{Path: *database, Kind: repository.Manager, SoftwareVersion: buildinfo.SoftwareVersion()}
	if *initialize {
		if err := sqlite.Initialize(context.Background(), options); err != nil {
			logger.Error("database_initialization_failed", "code", "STORAGE_INITIALIZATION_FAILED")
			return 1
		}
		logger.Info("database_initialized")
		return 0
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	store, storageErr := sqlite.Open(ctx, options)
	defer store.Close()
	if storageErr != nil {
		logger.Error("storage_degraded", "code", store.Status().Code)
	}
	if *initSecret {
		if storageErr != nil {
			logger.Error("security_action_failed", "code", "STORAGE_UNAVAILABLE")
			return 1
		}
		if _, err := secret.InitializeKeys(*secretDir, nil); err != nil {
			logger.Error("security_action_failed", "code", "SECRET_KEY_UNAVAILABLE")
			return 1
		}
		logger.Info("security_action_completed")
		return 0
	}
	var privileged *secrets.Store
	if _, err := os.Lstat(*secretDir); err == nil {
		keys, e := secret.LoadKeys(*secretDir)
		if e == nil && storageErr == nil {
			privileged, e = secrets.New(ctx, store, secret.Privileged, keys)
		}
		if e != nil || privileged == nil {
			logger.Error("service_start_failed", "code", "SECRET_KEY_UNAVAILABLE")
			return 1
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		logger.Error("service_start_failed", "code", "SECRET_KEY_UNAVAILABLE")
		return 1
	}
	if *rotateSecret {
		if privileged == nil {
			logger.Error("security_action_failed", "code", "SECRET_KEY_UNAVAILABLE")
			return 1
		}
		next, err := secret.PrepareKey(*secretDir)
		if err == nil {
			err = privileged.Rewrap(ctx, next)
		}
		if err == nil {
			err = secret.ActivateKeys(*secretDir, next.Active)
		}
		if err != nil {
			logger.Error("security_action_failed", "code", "SECRET_ROTATION_FAILED")
			return 1
		}
		logger.Info("security_action_completed")
		return 0
	}
	var authentication *auth.Repository
	if key, err := authn.KeyFile(*authKeyPath, false); err == nil && storageErr == nil {
		authentication, err = auth.New(store, key)
		if err != nil {
			logger.Error("authentication_degraded", "code", "AUTH_UNAVAILABLE")
		}
	} else {
		logger.Warn("authentication_degraded", "code", "AUTH_KEY_UNAVAILABLE")
	}
	if *bootstrap != "" {
		if authentication == nil {
			logger.Error("bootstrap_failed", "code", "AUTH_UNAVAILABLE")
			return 1
		}
		data, err := io.ReadAll(io.LimitReader(os.Stdin, 1027))
		if err != nil || len(data) > 1026 {
			logger.Error("bootstrap_failed", "code", "PASSWORD_INPUT_INVALID")
			return 1
		}
		password := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
		if err = authentication.Bootstrap(ctx, *bootstrap, password); err != nil {
			logger.Error("bootstrap_failed", "code", auth.ErrorCode(err))
			return 1
		}
		logger.Info("local_administrator_initialized")
		return 0
	}
	if *grantTreeRole != "" || *revokeTreeRole != "" {
		if authentication == nil {
			logger.Error("security_action_failed", "code", "AUTH_UNAVAILABLE")
			return 1
		}
		id, enabled := *grantTreeRole, true
		if *revokeTreeRole != "" {
			id, enabled = *revokeTreeRole, false
		}
		if err := authentication.SetSpanningTreeExecutionRole(ctx, id, enabled); err != nil {
			logger.Error("security_action_failed", "code", auth.ErrorCode(err))
			return 1
		}
		logger.Info("spanning_tree_role_policy_updated", "enabled", enabled)
		return 0
	}
	if *grantIsolationRole != "" || *revokeIsolationRole != "" {
		if authentication == nil {
			logger.Error("security_action_failed", "code", "AUTH_UNAVAILABLE")
			return 1
		}
		id, enabled := *grantIsolationRole, true
		if *revokeIsolationRole != "" {
			id, enabled = *revokeIsolationRole, false
		}
		if err := authentication.SetIsolationExecutionRole(ctx, id, enabled); err != nil {
			logger.Error("security_action_failed", "code", auth.ErrorCode(err))
			return 1
		}
		return 0
	}
	if *grantMirrorRole != "" || *revokeMirrorRole != "" {
		if authentication == nil {
			logger.Error("security_action_failed", "code", "AUTH_UNAVAILABLE")
			return 1
		}
		id, enabled := *grantMirrorRole, true
		if *revokeMirrorRole != "" {
			id, enabled = *revokeMirrorRole, false
		}
		if err := authentication.SetMirrorExecutionRole(ctx, id, enabled); err != nil {
			logger.Error("security_action_failed", "code", auth.ErrorCode(err))
			return 1
		}
		return 0
	}
	if *grantTunnelRole != "" || *revokeTunnelRole != "" {
		if authentication == nil {
			logger.Error("security_action_failed", "code", "AUTH_UNAVAILABLE")
			return 1
		}
		id, enabled := *grantTunnelRole, true
		if *revokeTunnelRole != "" {
			id, enabled = *revokeTunnelRole, false
		}
		if err := authentication.SetStaticTunnelExecutionRole(ctx, id, enabled); err != nil {
			logger.Error("security_action_failed", "code", auth.ErrorCode(err))
			return 1
		}
		return 0
	}
	if *grantMulticastRole != "" || *revokeMulticastRole != "" {
		if authentication == nil {
			logger.Error("security_action_failed", "code", "AUTH_UNAVAILABLE")
			return 1
		}
		id, enabled := *grantMulticastRole, true
		if *revokeMulticastRole != "" {
			id, enabled = *revokeMulticastRole, false
		}
		if err := authentication.SetMulticastExecutionRole(ctx, id, enabled); err != nil {
			logger.Error("security_action_failed", "code", auth.ErrorCode(err))
			return 1
		}
		return 0
	}
	if *restore {
		if authentication == nil {
			logger.Error("security_action_failed", "code", "AUTH_UNAVAILABLE")
			return 1
		}
		if privileged != nil {
			if err := privileged.Rewrap(ctx, privileged.Ring); err != nil {
				logger.Error("security_action_failed", "code", "SECRET_KEY_UNAVAILABLE")
				return 1
			}
		}
		if err := authentication.PrepareRestore(ctx); err != nil {
			logger.Error("security_action_failed", "code", "RESTORE_SECURITY_FAILED")
			return 1
		}
		logger.Info("security_action_completed")
		return 0
	}
	go store.Maintain(ctx)
	// Reconcile the persisted lease before accepting reads or advertising ready.
	// A one-second maintenance tick must not expose an expired trial as current
	// immediately after a manager restart.
	if authentication != nil {
		if _, err := authentication.TLSState(ctx); err != nil {
			logger.Error("service_start_failed", "code", "TLS_RECOVERY_UNAVAILABLE")
			return 1
		}
	}
	var inventoryService *inventory.Service
	var fields *executions.Engine
	if storageErr == nil {
		reg, err := registry.New(store)
		if err != nil {
			logger.Error("inventory_start_failed", "code", "INVENTORY_STORAGE_UNAVAILABLE")
			return 1
		}
		inventoryService = inventory.New(reg)
		var mirrorIDs, mirrorTargets []string
		if *localMirrorObjects != "" {
			mirrorIDs = strings.Split(*localMirrorObjects, ",")
		}
		if *localMirrorCreates != "" {
			mirrorTargets = strings.Split(*localMirrorCreates, ",")
		}
		if err = inventoryService.SetLocalMirrorObjects(mirrorIDs); err != nil {
			logger.Error("inventory_start_failed", "code", "INVALID_MIRROR_AUTHORITY")
			return 2
		}
		if err = inventoryService.SetLocalMirrorCreates(mirrorTargets); err != nil {
			logger.Error("inventory_start_failed", "code", "INVALID_MIRROR_CREATION_AUTHORITY")
			return 2
		}
		var tunnelIDs, tunnelTargets []string
		if *localTunnelObjects != "" {
			tunnelIDs = strings.Split(*localTunnelObjects, ",")
		}
		if *localTunnelCreates != "" {
			tunnelTargets = strings.Split(*localTunnelCreates, ",")
		}
		if err = inventoryService.SetLocalTunnelObjects(tunnelIDs); err != nil {
			logger.Error("inventory_start_failed", "code", "INVALID_TUNNEL_AUTHORITY")
			return 2
		}
		if err = inventoryService.SetLocalTunnelCreates(tunnelTargets); err != nil {
			logger.Error("inventory_start_failed", "code", "INVALID_TUNNEL_CREATION_AUTHORITY")
			return 2
		}
		var isolationIDs []string
		if *localIsolation != "" {
			isolationIDs = strings.Split(*localIsolation, ",")
		}
		if err = inventoryService.SetLocalIsolationPorts(isolationIDs); err != nil {
			logger.Error("inventory_start_failed", "code", "INVALID_ISOLATION_AUTHORITY")
			return 2
		}
		var multicastIDs []string
		if *localMulticast != "" {
			multicastIDs = strings.Split(*localMulticast, ",")
		}
		if err = inventoryService.SetLocalMulticastBridges(multicastIDs); err != nil {
			logger.Error("inventory_start_failed", "code", "INVALID_MULTICAST_AUTHORITY")
			return 2
		}
		linuxObserver := linuxprovider.New()
		inventoryService.SetDeviceObserver(linuxObserver)
		inventoryService.SetPolicingObserver(linuxObserver)
		var vlanIDs []string
		if *localVLANPorts != "" {
			vlanIDs = strings.Split(*localVLANPorts, ",")
		}
		spanningTreeIDs := []string{}
		if *localSpanningTree != "" {
			spanningTreeIDs = strings.Split(*localSpanningTree, ",")
		}
		if err = inventoryService.SetLocalSpanningTreeBridges(spanningTreeIDs); err != nil {
			logger.Error("inventory_start_failed", "code", "INVALID_SPANNING_TREE_AUTHORITY")
			return 2
		}
		if treeConfigured {
			if err = inventoryService.SetLocalSpanningTreeExecutionBridges(strings.Split(*localSpanningTreeExecution, ",")); err != nil {
				logger.Error("inventory_start_failed", "code", "INVALID_SPANNING_TREE_EXECUTION_AUTHORITY")
				return 2
			}
		}
		topologyIDs := []string{}
		if *localTopology != "" {
			topologyIDs = strings.Split(*localTopology, ",")
		}
		if err = inventoryService.SetLocalTopologyObjects(topologyIDs); err != nil {
			logger.Error("inventory_start_failed", "code", "INVALID_TOPOLOGY_AUTHORITY")
			return 2
		}
		topologyTargets := []string{}
		if *localTopologyCreates != "" {
			topologyTargets = strings.Split(*localTopologyCreates, ",")
		}
		if err = inventoryService.SetLocalTopologyCreates(topologyTargets); err != nil {
			logger.Error("inventory_start_failed", "code", "INVALID_TOPOLOGY_CREATION_AUTHORITY")
			return 2
		}
		if err = inventoryService.SetLocalVLANPorts(vlanIDs); err != nil {
			logger.Error("inventory_start_failed", "code", "INVALID_VLAN_AUTHORITY")
			return 2
		}
		var bondIDs []string
		if *localBondPorts != "" {
			bondIDs = strings.Split(*localBondPorts, ",")
		}
		if err = inventoryService.SetLocalBondPorts(bondIDs); err != nil {
			logger.Error("inventory_start_failed", "code", "INVALID_BOND_AUTHORITY")
			return 2
		}
		var mtuIDs []string
		if *localMTUInterfaces != "" {
			mtuIDs = strings.Split(*localMTUInterfaces, ",")
		}
		if err = inventoryService.SetLocalMTUInterfaces(mtuIDs); err != nil {
			logger.Error("inventory_start_failed", "code", "INVALID_MTU_AUTHORITY")
			return 2
		}
		var policingIDs []string
		if *localPolicingInterfaces != "" {
			policingIDs = strings.Split(*localPolicingInterfaces, ",")
		}
		if err = inventoryService.SetLocalPolicingInterfaces(policingIDs); err != nil {
			logger.Error("inventory_start_failed", "code", "INVALID_POLICING_AUTHORITY")
			return 2
		}
		var bridgeNames []string
		if *localBridgeNames != "" {
			bridgeNames = strings.Split(*localBridgeNames, ",")
		}
		if slices.Contains(bridgeNames, *probeInterface) {
			logger.Error("inventory_start_failed", "code", "BRIDGE_NAME_IS_MANAGEMENT_INTERFACE")
			return 2
		}
		if err = inventoryService.SetLocalBridgeNames(bridgeNames); err != nil {
			logger.Error("inventory_start_failed", "code", "INVALID_BRIDGE_CREATION_AUTHORITY")
			return 2
		}
		var bridgeDeleteNames []string
		if *localBridgeDeleteNames != "" {
			bridgeDeleteNames = strings.Split(*localBridgeDeleteNames, ",")
		}
		if slices.Contains(bridgeDeleteNames, *probeInterface) {
			logger.Error("inventory_start_failed", "code", "BRIDGE_NAME_IS_MANAGEMENT_INTERFACE")
			return 2
		}
		if err = inventoryService.SetLocalBridgeDeleteNames(bridgeDeleteNames); err != nil {
			logger.Error("inventory_start_failed", "code", "INVALID_BRIDGE_DELETION_AUTHORITY")
			return 2
		}
		var internalTargets []string
		if *localInternalPortTargets != "" {
			internalTargets = strings.Split(*localInternalPortTargets, ",")
		}
		for _, target := range internalTargets {
			_, name, _ := strings.Cut(target, ":")
			if name == *probeInterface {
				logger.Error("inventory_start_failed", "code", "PORT_NAME_IS_MANAGEMENT_INTERFACE")
				return 2
			}
		}
		if err = inventoryService.SetLocalInternalPortTargets(internalTargets); err != nil {
			logger.Error("inventory_start_failed", "code", "INVALID_INTERNAL_PORT_AUTHORITY")
			return 2
		}
		var internalDeleteTargets []string
		if *localInternalPortDeleteTargets != "" {
			internalDeleteTargets = strings.Split(*localInternalPortDeleteTargets, ",")
		}
		for _, target := range internalDeleteTargets {
			_, name, _ := strings.Cut(target, ":")
			if name == *probeInterface {
				logger.Error("inventory_start_failed", "code", "PORT_NAME_IS_MANAGEMENT_INTERFACE")
				return 2
			}
		}
		if err = inventoryService.SetLocalInternalPortDeleteTargets(internalDeleteTargets); err != nil {
			logger.Error("inventory_start_failed", "code", "INVALID_INTERNAL_PORT_AUTHORITY")
			return 2
		}
		if *ovsUID >= 1<<32-1 {
			logger.Error("inventory_start_failed", "code", "OVSDB_PEER_INVALID")
			return 2
		}
		provider, err := ovsprovider.New(ovsprovider.Options{Socket: *ovsSocket, DatabaseFile: *ovsFile, PeerUID: uint32(*ovsUID)})
		if err != nil {
			logger.Error("inventory_start_failed", "code", "OVSDB_ENDPOINT_INVALID")
			return 2
		}
		if mirrorConfigured {
			if err = provider.ConfigureMirrorExecution(ovsprovider.MirrorExecutionOptions{ControlSocket: *mirrorControl, Executable: *mirrorExecutable, Manifest: *mirrorManifest, ManifestDigest: *mirrorManifestDigest, PeerUID: uint32(*ovsUID)}); err != nil {
				logger.Error("service_start_failed", "code", "INVALID_MIRROR_RUNTIME_CONFIGURATION")
				return 2
			}
		}
		providerCtx, cancelProvider := context.WithCancel(ctx)
		defer cancelProvider()
		providerDone := make(chan struct{})
		go func() { defer close(providerDone); provider.Run(providerCtx, inventoryService) }()
		if *acceptEvidence != "" {
			deadline := time.NewTimer(8 * time.Second)
			defer deadline.Stop()
			tick := time.NewTicker(50 * time.Millisecond)
			defer tick.Stop()
			for !inventoryService.Observed() {
				select {
				case <-ctx.Done():
					return 1
				case <-deadline.C:
					logger.Error("reconciliation_failed", "code", "OVSDB_NOT_OBSERVED")
					return 1
				case <-tick.C:
				}
			}
			cancelProvider()
			<-providerDone
			if err = inventoryService.Accept(ctx, *acceptEvidence, *acceptReason); err != nil {
				logger.Error("reconciliation_failed", "code", auth.ErrorCode(err))
				return 1
			}
			logger.Info("inventory_reconciliation_completed", "configuration_written", false)
			return 0
		}
		if authentication != nil {
			authentication.WithInventory(inventoryService)
			if multicastGroupsConfigured {
				observer, observeErr := provider.MulticastGroupObserver(ovsprovider.MulticastGroupOptions{ControlSocket: *multicastControl, Executable: *multicastExecutable, Manifest: *multicastManifest, ManifestDigest: *multicastManifestDigest, PeerUID: uint32(*ovsUID)})
				if observeErr != nil {
					logger.Error("service_start_failed", "code", "INVALID_MULTICAST_GROUP_SOURCE")
					return 2
				}
				inventoryService.SetMulticastGroupObserver(observer)
			}
			executor := provider.Executor(inventoryService)
			if treeConfigured {
				if err = executor.ConfigureSpanningTreeExecution(ovsprovider.SpanningTreeExecutionOptions{ControlSocket: *treeControl, Executable: *treeExecutable, Manifest: *treeManifest, ManifestDigest: *treeManifestDigest, DatabaseExecutable: *treeDatabaseExecutable, DatabaseSHA256: *treeDatabaseSHA256, PeerUID: uint32(*ovsUID)}); err != nil {
					logger.Error("service_start_failed", "code", "INVALID_SPANNING_TREE_WITNESS_CONFIGURATION")
					return 2
				}
			}
			fields, err = authentication.ConfigureExecution(executor)
			if err != nil || fields.Recover(ctx) != nil {
				logger.Error("service_start_failed", "code", "EXECUTION_RECOVERY_UNAVAILABLE")
				return 1
			}
			if *probeAddress != "" || *probeInterface != "" {
				probe, err := safety.NewTCPProbe(*probeAddress, *probeInterface)
				if err != nil || authentication.ConfigureSafety(executions.SafetyOptions{Probe: probe}) != nil {
					logger.Error("service_start_failed", "code", "INVALID_SAFE_APPLY_CONFIGURATION")
					return 2
				}
			}
		}
	} else if *acceptEvidence != "" {
		logger.Error("reconciliation_failed", "code", "INVENTORY_STORAGE_UNAVAILABLE")
		return 1
	}
	if storageErr == nil {
		configured := authentication != nil && authentication.SafetyAvailable()
		if err := executions.RequireSafetyConfiguration(ctx, store, configured); err != nil {
			logger.Error("service_start_failed", "code", "SAFE_APPLY_RECOVERY_CONFIGURATION_REQUIRED")
			return 1
		}
	}
	listener, err := ipc.ListenUnix(ipc.SocketOptions{Path: *socket, OwnerUID: 0, GroupGID: uint32(*gid), PeerUID: uint32(*uid)})
	if err != nil {
		logger.Error("service_start_failed", "code", "IPC_LISTENER_UNAVAILABLE")
		return 1
	}
	defer listener.Release()
	var authorizer ipc.Authorizer
	var authService authn.Manager
	if authentication != nil {
		authorizer = authentication
		authService = authentication
	}
	handler := ipc.NewHandler(ipc.CurrentProtocol(buildinfo.SoftwareVersion()), authorizer, logger, func(ctx context.Context) ipc.Health {
		health := ipc.BootstrapHealth()
		status := store.Probe(ctx)
		health.Storage = &ipc.StorageHealth{Manager: &status}
		health.AuthenticationReady = authentication != nil && authentication.Ready(ctx)
		if !status.Writable {
			health.State = "degraded"
		}
		health.ConfigurationReady = authentication != nil && authentication.SafetyAvailable() && status.Writable
		return health
	}).WithAuthentication(authService)
	if authentication != nil {
		if err := authentication.InitializeEvidence(ctx); err != nil {
			logger.Error("service_start_failed", "code", auth.ErrorCode(err))
			return 1
		}
		handler.WithTLS(authentication)
		go authentication.MaintainTLS(ctx)
		go authentication.MaintainEvidence(ctx)
	}
	server := ipc.HTTPServer(handler)
	// Recovery progresses independently from every HTTP/IPC admission queue.
	// Only this loop may emit watchdog heartbeats. A database/loop failure lets
	// systemd restart mgrd; it never signals ovsdb-server or ovs-vswitchd.
	go func() {
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
			cycle, cancel := context.WithTimeout(ctx, 8*time.Second)
			err := error(nil)
			if fields != nil {
				err = fields.Recover(cycle)
				if err == nil {
					err = fields.SafetyTick(cycle)
				}
			}
			if err == nil && store.Status().Writable {
				_ = runtimehost.Notify("WATCHDOG=1")
			}
			cancel()
		}
	}()
	if err := runtimehost.Notify("READY=1"); err != nil {
		logger.Error("service_start_failed", "code", "SUPERVISOR_NOTIFY_FAILED")
		return 1
	}
	logger.Info("service_started", "scope", "runtime-bootstrap", "configuration_ready", false)
	if err := runtimehost.Run(ctx, server, func() error { return server.Serve(listener) }, logger); err != nil {
		logger.Error("service_stopped", "code", "HTTP_SERVE_FAILED")
		return 1
	}
	return 0
}
