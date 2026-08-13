package service

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/sharif102007/4x-ui/v2/database"
	"github.com/sharif102007/4x-ui/v2/database/model"
	"github.com/sharif102007/4x-ui/v2/logger"
)

// SshManagerService is the orchestration layer for the SSH Manager feature. It
// owns the database rows and reconciles the live host (OpenSSH drop-in,
// stunnel, payload gateways, firewall) to match the enabled inbounds.
type SshManagerService struct {
	settingService SettingService
}

var sshSys sshSystem

// Runtime state for the in-process payload gateways. The service structs in
// this project are zero-value/stateless and created ad hoc, so the running
// gateways live in package-level state guarded by a mutex.
var (
	sshRuntimeMu   sync.Mutex
	sshReconcileMu sync.Mutex
	sshGateways    = map[int]*payloadGateway{} // key: inbound ID
)

// gatewaySpec holds the config needed to start/compare a payload gateway.
type gatewaySpec struct {
	bindIP  string
	listen  int
	backend int
}

// ---------------------------------------------------------------------------
// Password encryption (AES-256-GCM, key derived from the panel secret)
// ---------------------------------------------------------------------------

func (s *SshManagerService) secretKey() ([]byte, error) {
	secret, err := s.settingService.GetSecret()
	if err != nil {
		return nil, err
	}
	return deriveKey(string(secret)), nil
}

func (s *SshManagerService) encryptPassword(plain string) (string, error) {
	key, err := s.secretKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return base64.StdEncoding.EncodeToString(ct), nil
}

func (s *SshManagerService) decryptPassword(enc string) (string, error) {
	if enc == "" {
		return "", nil
	}
	key, err := s.secretKey()
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	ns := gcm.NonceSize()
	if len(raw) < ns {
		return "", errors.New("ciphertext too short")
	}
	plain, err := gcm.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// ---------------------------------------------------------------------------
// Inbound validation & port-conflict checking
// ---------------------------------------------------------------------------

func validMode(m string) bool {
	switch m {
	case model.SshModeNormal, model.SshModeTlsSni, model.SshModeTlsPayload, model.SshModePayloadOnly:
		return true
	}
	return false
}

func (s *SshManagerService) validateInbound(in *model.SshInbound) error {
	if strings.TrimSpace(in.Name) == "" {
		return errors.New("name is required")
	}
	if !validMode(in.Mode) {
		return errors.New("invalid mode")
	}
	if err := validPort(in.ListenPort); err != nil {
		return err
	}
	switch in.Mode {
	case model.SshModeNormal:
		// public port IS the backend port
		in.BackendSshPort = in.ListenPort
		in.GatewayPort = 0

	case model.SshModePayloadOnly:
		// plain TCP payload gateway — no TLS, no cert fields needed
		if err := validPort(in.BackendSshPort); err != nil {
			return fmt.Errorf("backend ssh port: %v", err)
		}
		if in.BackendSshPort == in.ListenPort {
			return errors.New("backend ssh port must differ from the public listen port")
		}
		in.GatewayPort = 0
		in.CertMode = ""
		in.CertFile = ""
		in.KeyFile = ""

	default: // ssh_tls_sni, ssh_tls_payload
		if err := validPort(in.BackendSshPort); err != nil {
			return fmt.Errorf("backend ssh port: %v", err)
		}
		if in.BackendSshPort == in.ListenPort {
			return errors.New("backend ssh port must differ from the public TLS port")
		}
		switch in.CertMode {
		case model.SshCertExisting:
			cf, err := cleanCertPath(in.CertFile)
			if err != nil {
				return fmt.Errorf("cert file: %v", err)
			}
			kf, err := cleanCertPath(in.KeyFile)
			if err != nil {
				return fmt.Errorf("key file: %v", err)
			}
			in.CertFile, in.KeyFile = cf, kf
		case model.SshCertSelfSigned, "":
			in.CertMode = model.SshCertSelfSigned
		default:
			return errors.New("invalid certificate mode")
		}
		if in.Mode == model.SshModeTlsSni {
			in.GatewayPort = 0
		} else if in.GatewayPort > 0 {
			if err := validPort(in.GatewayPort); err != nil {
				return fmt.Errorf("payload gateway port: %v", err)
			}
		}
	}
	if in.UdpRelayPort > 0 {
		if err := validPort(in.UdpRelayPort); err != nil {
			return fmt.Errorf("UDP relay port: %v", err)
		}
	}
	return nil
}

// collectReservedPorts returns every port already spoken for by other SSH
// inbounds, the panel itself and Xray inbounds, so a new/edited inbound cannot
// silently clash with anything (including 4x-ui/Xray TLS ports).
func (s *SshManagerService) collectReservedPorts(excludeInboundID int) map[int]string {
	reserved := map[int]string{}
	db := database.GetDB()

	var others []model.SshInbound
	db.Find(&others)
	for _, o := range others {
		if o.Id == excludeInboundID {
			continue
		}
		if o.ListenPort > 0 {
			reserved[o.ListenPort] = "another SSH inbound (" + o.Name + ")"
		}
		if o.BackendSshPort > 0 {
			reserved[o.BackendSshPort] = "another SSH inbound backend (" + o.Name + ")"
		}
		if o.GatewayPort > 0 {
			reserved[o.GatewayPort] = "another SSH inbound gateway (" + o.Name + ")"
		}
		if o.UdpRelayPort > 0 {
			reserved[o.UdpRelayPort] = "another SSH inbound UDP relay (" + o.Name + ")"
		}
	}

	// Xray inbounds
	var xinb []model.Inbound
	db.Model(&model.Inbound{}).Find(&xinb)
	for _, x := range xinb {
		if x.Port > 0 {
			reserved[x.Port] = "an Xray inbound"
		}
	}

	// Panel + subscription ports
	if p, err := s.settingService.GetPort(); err == nil && p > 0 {
		reserved[p] = "the 4x-ui panel"
	}
	if p, err := s.settingService.GetSubPort(); err == nil && p > 0 {
		reserved[p] = "the subscription server"
	}
	return reserved
}

// CheckPortConflict validates one candidate public port for the UI pre-check.
func (s *SshManagerService) CheckPortConflict(port, excludeInboundID int) error {
	if err := validPort(port); err != nil {
		return err
	}
	reserved := s.collectReservedPorts(excludeInboundID)
	if reason, clash := reserved[port]; clash {
		return fmt.Errorf("port %d is already used by %s", port, reason)
	}
	// Live probe (skip if it is the existing public port of the edited inbound).
	if excludeInboundID > 0 {
		if cur, err := s.GetInbound(excludeInboundID); err == nil && cur.ListenPort == port && cur.Enable {
			return nil
		}
	}
	if !sshSys.portFree(port) {
		return fmt.Errorf("port %d is currently in use by another process", port)
	}
	return nil
}

// checkInboundPorts validates all ports an inbound wants to occupy.
func (s *SshManagerService) checkInboundPorts(in *model.SshInbound) error {
	reserved := s.collectReservedPorts(in.Id)
	withinInbound := map[int]string{}

	check := func(p int, label string) error {
		if p <= 0 {
			return nil
		}
		if reason, clash := reserved[p]; clash {
			return fmt.Errorf("%s port %d is already used by %s", label, p, reason)
		}
		if previous, clash := withinInbound[p]; clash {
			return fmt.Errorf("%s port %d conflicts with this inbound's %s port", label, p, previous)
		}
		withinInbound[p] = label
		return nil
	}
	if err := check(in.ListenPort, "public"); err != nil {
		return err
	}
	if in.Mode != model.SshModeNormal {
		if err := check(in.BackendSshPort, "backend"); err != nil {
			return err
		}
	}
	if in.GatewayPort > 0 {
		if err := check(in.GatewayPort, "gateway"); err != nil {
			return err
		}
	}
	if in.UdpRelayPort > 0 {
		if err := check(in.UdpRelayPort, "UDP relay"); err != nil {
			return err
		}
	}

	// Live probe of the public port if it is new to us.
	prevSamePublic := false
	if in.Id > 0 {
		if cur, err := s.GetInbound(in.Id); err == nil && cur.ListenPort == in.ListenPort && cur.Enable {
			prevSamePublic = true
		}
	}
	if !prevSamePublic && !sshSys.portFree(in.ListenPort) {
		return fmt.Errorf("public port %d is currently in use by another process", in.ListenPort)
	}
	if in.UdpRelayPort > 0 {
		prevSameRelay := false
		if in.Id > 0 {
			if cur, err := s.GetInbound(in.Id); err == nil && cur.UdpRelayPort == in.UdpRelayPort && cur.Enable {
				prevSameRelay = true
			}
		}
		if !prevSameRelay && !sshSys.portFree(in.UdpRelayPort) {
			return fmt.Errorf("UDP relay port %d is currently in use by another process", in.UdpRelayPort)
		}
	}
	return nil
}

func (s *SshManagerService) allocateGatewayPort(in *model.SshInbound) (int, error) {
	reserved := s.collectReservedPorts(in.Id)
	for _, port := range []int{in.ListenPort, in.BackendSshPort, in.UdpRelayPort} {
		if port > 0 {
			reserved[port] = "this inbound"
		}
	}
	for attempt := 0; attempt < 32; attempt++ {
		port, err := sshSys.freeLocalPort()
		if err != nil {
			return 0, err
		}
		if _, clash := reserved[port]; !clash {
			return port, nil
		}
	}
	return 0, errors.New("could not allocate a unique payload gateway port")
}

// ---------------------------------------------------------------------------
// Inbound CRUD
// ---------------------------------------------------------------------------

func (s *SshManagerService) GetInbounds() ([]model.SshInbound, error) {
	var list []model.SshInbound
	err := database.GetDB().Order("id asc").Find(&list).Error
	return list, err
}

func (s *SshManagerService) GetInbound(id int) (*model.SshInbound, error) {
	in := &model.SshInbound{}
	err := database.GetDB().First(in, id).Error
	if err != nil {
		return nil, err
	}
	return in, nil
}

func (s *SshManagerService) AddInbound(in *model.SshInbound) (*model.SshInbound, error) {
	if err := s.validateInbound(in); err != nil {
		return nil, err
	}
	if err := s.checkInboundPorts(in); err != nil {
		return nil, err
	}
	// Auto-assign a loopback gateway port for payload mode.
	if in.Mode == model.SshModeTlsPayload && in.GatewayPort == 0 {
		gp, err := s.allocateGatewayPort(in)
		if err != nil {
			return nil, err
		}
		in.GatewayPort = gp
	}
	in.Id = 0
	if err := database.GetDB().Create(in).Error; err != nil {
		return nil, err
	}
	logger.Infof("ssh-manager: inbound created id=%d name=%q mode=%s port=%d", in.Id, in.Name, in.Mode, in.ListenPort)
	if err := s.Reconcile(); err != nil {
		rollbackErr := database.GetDB().Delete(&model.SshInbound{}, in.Id).Error
		restoreErr := s.Reconcile()
		if rollbackErr != nil || restoreErr != nil {
			return nil, fmt.Errorf("create reconcile failed: %v; database rollback: %v; live rollback: %v", err, rollbackErr, restoreErr)
		}
		return nil, fmt.Errorf("create reconcile failed; inbound rolled back: %v", err)
	}
	return in, nil
}

func (s *SshManagerService) UpdateInbound(in *model.SshInbound) (*model.SshInbound, error) {
	cur, err := s.GetInbound(in.Id)
	if err != nil {
		return nil, errors.New("inbound not found")
	}
	if err := s.validateInbound(in); err != nil {
		return nil, err
	}
	if err := s.checkInboundPorts(in); err != nil {
		return nil, err
	}
	if in.Mode == model.SshModeTlsPayload {
		if in.GatewayPort == 0 {
			if cur.GatewayPort != 0 {
				in.GatewayPort = cur.GatewayPort
			} else {
				gp, err := s.allocateGatewayPort(in)
				if err != nil {
					return nil, err
				}
				in.GatewayPort = gp
			}
		}
	} else {
		in.GatewayPort = 0
	}
	in.CreatedAt = cur.CreatedAt
	if err := database.GetDB().Save(in).Error; err != nil {
		return nil, err
	}
	logger.Infof("ssh-manager: inbound updated id=%d name=%q mode=%s port=%d", in.Id, in.Name, in.Mode, in.ListenPort)
	if err := s.Reconcile(); err != nil {
		rollbackErr := database.GetDB().Save(cur).Error
		restoreErr := s.Reconcile()
		if rollbackErr != nil || restoreErr != nil {
			return nil, fmt.Errorf("update reconcile failed: %v; database rollback: %v; live rollback: %v", err, rollbackErr, restoreErr)
		}
		return nil, fmt.Errorf("update reconcile failed; previous inbound restored: %v", err)
	}
	return in, nil
}

func (s *SshManagerService) DelInbound(id int) error {
	cur, err := s.GetInbound(id)
	if err != nil {
		return errors.New("inbound not found")
	}
	if err := database.GetDB().Delete(&model.SshInbound{}, id).Error; err != nil {
		return err
	}
	logger.Infof("ssh-manager: inbound deleted id=%d", id)
	if err := s.Reconcile(); err != nil {
		// Keep the database and live host consistent when OpenSSH/stunnel rejects
		// a new configuration. Restore the row with its original primary key.
		if restoreErr := database.GetDB().Create(cur).Error; restoreErr != nil {
			return fmt.Errorf("delete reconcile failed: %v; database rollback failed: %v", err, restoreErr)
		}
		_ = s.Reconcile()
		return fmt.Errorf("delete reconcile failed; inbound restored: %v", err)
	}
	return nil
}

func (s *SshManagerService) SetInboundEnable(id int, enable bool) error {
	cur, err := s.GetInbound(id)
	if err != nil {
		return errors.New("inbound not found")
	}
	if cur.Enable == enable {
		return nil
	}
	if err := database.GetDB().Model(&model.SshInbound{}).Where("id = ?", id).Update("enable", enable).Error; err != nil {
		return err
	}
	logger.Infof("ssh-manager: inbound id=%d enable=%v", id, enable)
	if err := s.Reconcile(); err != nil {
		rollbackErr := database.GetDB().Model(&model.SshInbound{}).Where("id = ?", id).Update("enable", cur.Enable).Error
		restoreErr := s.Reconcile()
		if rollbackErr != nil || restoreErr != nil {
			return fmt.Errorf("enable reconcile failed: %v; database rollback: %v; live rollback: %v", err, rollbackErr, restoreErr)
		}
		return fmt.Errorf("enable reconcile failed; previous state restored: %v", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// User CRUD (backed by real Linux accounts in the xui-ssh-users group)
// ---------------------------------------------------------------------------

func (s *SshManagerService) GetUsers() ([]model.SshUser, error) {
	var list []model.SshUser
	if err := database.GetDB().Order("id asc").Find(&list).Error; err != nil {
		return nil, err
	}
	// Decrypt passwords for display (panel-admin only context).
	for i := range list {
		if pw, err := s.decryptPassword(list[i].PasswordEnc); err == nil {
			list[i].Password = pw
		}
	}
	return list, nil
}

func (s *SshManagerService) AddUser(u *model.SshUser) error {
	if err := validateUserLimits(u); err != nil {
		return err
	}
	u.Username = strings.TrimSpace(u.Username)
	if err := validUsername(u.Username); err != nil {
		return err
	}
	if isProtectedUser(u.Username) {
		return errors.New("refusing to manage a protected system user")
	}
	if err := validPassword(u.Password); err != nil {
		return err
	}
	enc, err := s.encryptPassword(u.Password)
	if err != nil {
		return err
	}
	// DB uniqueness
	var count int64
	if err := database.GetDB().Model(&model.SshUser{}).Where("username = ?", u.Username).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return errors.New("a managed user with that name already exists")
	}
	if sshSys.userExists(u.Username) {
		return errors.New("a system user with that name already exists")
	}

	if err := sshSys.ensureGroup(); err != nil {
		return err
	}
	if err := sshSys.createUser(u.Username); err != nil {
		return err
	}
	createdSystemUser := true
	defer func() {
		if createdSystemUser {
			_ = sshSys.deleteUser(u.Username)
		}
	}()
	if err := sshSys.setPassword(u.Username, u.Password); err != nil {
		return err
	}
	if !u.Enable {
		if err := sshSys.lockUser(u.Username); err != nil {
			return err
		}
	} else {
		if err := sshSys.setExpiry(u.Username, msToChageDate(u.ExpiryTime)); err != nil {
			return err
		}
	}
	row := model.SshUser{
		Username: u.Username, PasswordEnc: enc, Enable: u.Enable, ExpiryTime: u.ExpiryTime, Note: u.Note,
		TrafficLimit: u.TrafficLimit, ResetFlow: u.ResetFlow, LastResetTime: time.Now().UnixMilli(),
		SpeedLimit: u.SpeedLimit, DownloadMbps: u.DownloadMbps, UploadMbps: u.UploadMbps,
	}
	if err := database.GetDB().Create(&row).Error; err != nil {
		return err
	}
	createdSystemUser = false
	u.Id = row.Id
	logger.Infof("ssh-manager: user created %q (enabled=%v)", u.Username, u.Enable)
	return nil
}

func (s *SshManagerService) UpdateUser(u *model.SshUser) error {
	if err := validateUserLimits(u); err != nil {
		return err
	}
	cur := &model.SshUser{}
	if err := database.GetDB().First(cur, u.Id).Error; err != nil {
		return errors.New("user not found")
	}
	if isProtectedUser(cur.Username) {
		return errors.New("refusing to manage a protected system user")
	}
	if !sshSys.userExists(cur.Username) {
		return errors.New("managed system user is missing")
	}
	// Guard: only ever touch accounts that are members of our group.
	if !sshSys.inManagedGroup(cur.Username) {
		return errors.New("system user is not managed by the panel (not in " + sshUsersGroup + ")")
	}

	original := *cur
	passwordChanged := strings.TrimSpace(u.Password) != ""
	oldPassword := ""
	if passwordChanged {
		if err := validPassword(u.Password); err != nil {
			return err
		}
		var err error
		oldPassword, err = s.decryptPassword(original.PasswordEnc)
		if err != nil || oldPassword == "" {
			return errors.New("cannot safely change password because the stored password cannot be decrypted")
		}
		enc, err := s.encryptPassword(u.Password)
		if err != nil {
			return err
		}
		cur.PasswordEnc = enc
	}

	cur.Enable = u.Enable
	cur.ExpiryTime = u.ExpiryTime
	cur.Note = u.Note
	cur.TrafficLimit = u.TrafficLimit
	cur.ResetFlow = u.ResetFlow
	cur.SpeedLimit = u.SpeedLimit
	cur.DownloadMbps = u.DownloadMbps
	cur.UploadMbps = u.UploadMbps

	if err := database.GetDB().Save(cur).Error; err != nil {
		return err
	}

	applyErr := error(nil)
	if passwordChanged {
		applyErr = sshSys.setPassword(cur.Username, u.Password)
	}
	if applyErr == nil {
		if u.Enable {
			applyErr = sshSys.unlockUser(cur.Username, msToChageDate(u.ExpiryTime))
		} else {
			applyErr = sshSys.lockUser(cur.Username)
		}
	}
	if applyErr != nil {
		dbRollbackErr := database.GetDB().Save(&original).Error
		var passwordRollbackErr error
		if passwordChanged {
			passwordRollbackErr = sshSys.setPassword(original.Username, oldPassword)
		}
		var stateRollbackErr error
		if original.Enable {
			stateRollbackErr = sshSys.unlockUser(original.Username, msToChageDate(original.ExpiryTime))
		} else {
			stateRollbackErr = sshSys.lockUser(original.Username)
		}
		if dbRollbackErr != nil || passwordRollbackErr != nil || stateRollbackErr != nil {
			return fmt.Errorf("apply system user update failed: %v; database rollback: %v; password rollback: %v; state rollback: %v", applyErr, dbRollbackErr, passwordRollbackErr, stateRollbackErr)
		}
		return fmt.Errorf("apply system user update failed; previous state restored: %v", applyErr)
	}
	logger.Infof("ssh-manager: user updated %q (enabled=%v)", cur.Username, cur.Enable)
	return nil
}

func (s *SshManagerService) SetUserEnable(id int, enable bool) error {
	cur := &model.SshUser{}
	if err := database.GetDB().First(cur, id).Error; err != nil {
		return errors.New("user not found")
	}
	if isProtectedUser(cur.Username) {
		return errors.New("refusing to manage a protected system user")
	}
	if sshSys.userExists(cur.Username) && !sshSys.inManagedGroup(cur.Username) {
		return errors.New("system user is not managed by the panel")
	}
	if cur.Enable == enable {
		return nil
	}
	if enable {
		if err := sshSys.unlockUser(cur.Username, msToChageDate(cur.ExpiryTime)); err != nil {
			return err
		}
	} else {
		if err := sshSys.lockUser(cur.Username); err != nil {
			return err
		}
	}
	if err := database.GetDB().Model(&model.SshUser{}).Where("id = ?", id).Update("enable", enable).Error; err != nil {
		if cur.Enable {
			_ = sshSys.unlockUser(cur.Username, msToChageDate(cur.ExpiryTime))
		} else {
			_ = sshSys.lockUser(cur.Username)
		}
		return err
	}
	logger.Infof("ssh-manager: user id=%d enable=%v", id, enable)
	return nil
}

func (s *SshManagerService) DelUser(id int) error {
	cur := &model.SshUser{}
	if err := database.GetDB().First(cur, id).Error; err != nil {
		return errors.New("user not found")
	}
	if isProtectedUser(cur.Username) {
		return errors.New("refusing to delete a protected system user")
	}
	// Verify ownership before changing either side.
	if sshSys.userExists(cur.Username) {
		if !sshSys.inManagedGroup(cur.Username) {
			return errors.New("system user is not managed by the panel; not deleting")
		}
	}
	if err := database.GetDB().Delete(&model.SshUser{}, id).Error; err != nil {
		return err
	}
	if sshSys.userExists(cur.Username) {
		if err := sshSys.deleteUser(cur.Username); err != nil {
			if restoreErr := database.GetDB().Create(cur).Error; restoreErr != nil {
				return fmt.Errorf("delete system user failed: %v; database rollback failed: %v", err, restoreErr)
			}
			return fmt.Errorf("delete system user failed; database row restored: %v", err)
		}
	}
	logger.Infof("ssh-manager: user deleted %q", cur.Username)
	return nil
}

// ---------------------------------------------------------------------------
// Reconcile: make the live host match the enabled inbounds.
// ---------------------------------------------------------------------------

func (s *SshManagerService) Reconcile() error {
	sshReconcileMu.Lock()
	defer sshReconcileMu.Unlock()

	inbounds, err := s.GetInbounds()
	if err != nil {
		return err
	}

	var sshdPorts []int
	var stunnelSvcs []stunnelSvc
	desiredGateways := map[int]gatewaySpec{} // key: inbound ID
	banners := map[int]string{}              // sshd local port -> banner file
	udpRelayPorts := map[int]int{}           // inbound ID -> udpgw port

	for i := range inbounds {
		in := inbounds[i]
		if !in.Enable {
			continue
		}
		// Persist an optional banner and map it to the sshd-side port the
		// client's session actually lands on.
		if strings.TrimSpace(in.Banner) != "" {
			bp, bannerErr := sshSys.writeBanner(in.Id, in.Banner)
			if bannerErr != nil {
				return fmt.Errorf("persist banner for inbound %d: %w", in.Id, bannerErr)
			}
			if bp != "" {
				sshdLocalPort := in.ListenPort
				if in.Mode != model.SshModeNormal {
					sshdLocalPort = in.BackendSshPort
				}
				banners[sshdLocalPort] = bp
			}
		}
		// Optional UDP relay (badvpn-udpgw) — runs on loopback, reachable via SSH tunnel.
		if in.UdpRelayPort > 0 {
			udpRelayPorts[in.Id] = in.UdpRelayPort
		}

		switch in.Mode {
		case model.SshModeNormal:
			sshdPorts = append(sshdPorts, in.ListenPort)
			sshSys.allowPort(in.ListenPort)

		case model.SshModeTlsSni:
			sshdPorts = append(sshdPorts, in.BackendSshPort)
			cf, kf, cerr := s.resolveCert(&in)
			if cerr != nil {
				return fmt.Errorf("inbound %d certificate: %w", in.Id, cerr)
			}
			stunnelSvcs = append(stunnelSvcs, stunnelSvc{
				Name:        fmt.Sprintf("svc-%d", in.Id),
				AcceptPort:  in.ListenPort,
				ConnectPort: in.BackendSshPort,
				CertFile:    cf,
				KeyFile:     kf,
			})
			sshSys.allowPort(in.ListenPort)

		case model.SshModeTlsPayload:
			sshdPorts = append(sshdPorts, in.BackendSshPort)
			cf, kf, cerr := s.resolveCert(&in)
			if cerr != nil {
				return fmt.Errorf("inbound %d certificate: %w", in.Id, cerr)
			}
			stunnelSvcs = append(stunnelSvcs, stunnelSvc{
				Name:        fmt.Sprintf("svc-%d", in.Id),
				AcceptPort:  in.ListenPort,
				ConnectPort: in.GatewayPort,
				CertFile:    cf,
				KeyFile:     kf,
			})
			desiredGateways[in.Id] = gatewaySpec{bindIP: "127.0.0.1", listen: in.GatewayPort, backend: in.BackendSshPort}
			sshSys.allowPort(in.ListenPort)

		case model.SshModePayloadOnly:
			// Plain TCP: payload gateway binds directly on the public port — no stunnel, no TLS.
			sshdPorts = append(sshdPorts, in.BackendSshPort)
			desiredGateways[in.Id] = gatewaySpec{bindIP: "0.0.0.0", listen: in.ListenPort, backend: in.BackendSshPort}
			sshSys.allowPort(in.ListenPort)
		}
	}

	// 1) OpenSSH ports (validated + rolled back inside applySshdPorts).
	if err := sshSys.applySshdPorts(sshdPorts, banners); err != nil {
		return err
	}

	// 2) Payload gateways: start desired, stop the rest.
	if err := s.reconcileGateways(desiredGateways); err != nil {
		return err
	}

	// 3) stunnel services.
	if len(stunnelSvcs) > 0 && !sshSys.stunnelInstalled() {
		return errors.New("TLS inbound requires stunnel (install stunnel4)")
	} else {
		if err := sshSys.writeStunnel(stunnelSvcs); err != nil {
			return err
		}
	}

	// 4) UDP relay (badvpn-udpgw) — install if needed, then reconcile.
	if len(udpRelayPorts) > 0 {
		if err := EnsureBadvpn(); err != nil {
			return fmt.Errorf("UDP relay is enabled but badvpn is unavailable: %w", err)
		}
	}
	if err := reconcileUdpRelays(udpRelayPorts); err != nil {
		return err
	}

	logger.Infof("ssh-manager: reconciled (%d ssh ports, %d stunnel svc, %d gateways, %d udpgw)",
		len(sshdPorts), len(stunnelSvcs), len(desiredGateways), len(udpRelayPorts))
	return nil
}

func (s *SshManagerService) reconcileGateways(desired map[int]gatewaySpec) error {
	sshRuntimeMu.Lock()
	defer sshRuntimeMu.Unlock()

	// Stop gateways no longer desired or whose spec changed.
	for id, g := range sshGateways {
		want, ok := desired[id]
		if !ok || want.listen != g.listen || want.backend != g.backend || want.bindIP != g.bindIP {
			g.stop()
			delete(sshGateways, id)
		}
	}
	var firstErr error
	// Start newly desired gateways.
	for id, spec := range desired {
		if _, running := sshGateways[id]; running {
			continue
		}
		g := newPayloadGateway(id, spec.bindIP, spec.listen, spec.backend)
		if err := g.start(); err != nil {
			logger.Warningf("ssh-manager: gateway start failed for inbound %d: %v", id, err)
			if firstErr == nil {
				firstErr = fmt.Errorf("payload gateway for inbound %d: %w", id, err)
			}
			continue
		}
		sshGateways[id] = g
	}
	return firstErr
}

func (s *SshManagerService) resolveCert(in *model.SshInbound) (string, string, error) {
	if in.CertMode == model.SshCertExisting {
		cf, err := cleanCertPath(in.CertFile)
		if err != nil {
			return "", "", err
		}
		kf, err := cleanCertPath(in.KeyFile)
		if err != nil {
			return "", "", err
		}
		return cf, kf, nil
	}
	return sshSys.selfSignedCert(in.Id, in.Host)
}

// ---------------------------------------------------------------------------
// Runtime lifecycle (called from web server start/stop)
// ---------------------------------------------------------------------------

// InitRuntime brings the host into line with the stored config at panel start.
// Failures are logged but never abort panel startup.
func (s *SshManagerService) InitRuntime() {
	if err := sshSys.ensureGroup(); err != nil {
		logger.Warning("ssh-manager: ensure group failed:", err)
	}
	if err := s.Reconcile(); err != nil {
		logger.Warning("ssh-manager: initial reconcile failed:", err)
	}
	s.startLimitRuntime()
}

// StopRuntime tears down the in-process payload gateways and UDP relays on shutdown.
func (s *SshManagerService) StopRuntime() {
	stopLimitRuntime()
	sshRuntimeMu.Lock()
	defer sshRuntimeMu.Unlock()
	for port, g := range sshGateways {
		g.stop()
		delete(sshGateways, port)
	}
	StopAllUdpRelays()
}

// SystemStatus reports environment facts the UI surfaces to the admin.
func (s *SshManagerService) SystemStatus() map[string]any {
	return map[string]any{
		"stunnelInstalled": sshSys.stunnelInstalled(),
		"badvpnInstalled":  BadvpnInstalled(),
		"group":            sshUsersGroup,
	}
}
