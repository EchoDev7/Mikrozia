package panel

import (
	"errors"
	"strings"

	"gorm.io/gorm"

	"github.com/echodev7/mikrozia/v3/internal/config"
	"github.com/echodev7/mikrozia/v3/internal/database"
	"github.com/echodev7/mikrozia/v3/internal/database/model"
	"github.com/echodev7/mikrozia/v3/internal/util/crypto"
)

// AdminCredentialSource names where the administrator credential comes from.
type AdminCredentialSource string

const (
	// AdminCredentialFromEnvironment means the stored account was reconciled
	// from the RouterOS Container Environment values.
	AdminCredentialFromEnvironment AdminCredentialSource = "environment"
	// AdminCredentialPreserved means an existing account was kept unchanged
	// because the Environment does not configure one.
	AdminCredentialPreserved AdminCredentialSource = "preserved"
	// AdminCredentialLegacyDefault means the stored account still holds the
	// legacy insecure default credential and must not stay usable.
	AdminCredentialLegacyDefault AdminCredentialSource = "legacy-default"
	// AdminCredentialMissing means no account exists and none was configured.
	AdminCredentialMissing AdminCredentialSource = "missing"
)

// AdminCredentialState reports the outcome of inspecting the administrator
// credential and whether this call changed the stored account.
type AdminCredentialState struct {
	Source  AdminCredentialSource
	Managed bool
	Changed bool
}

// ErrAdminCredentialPairIncomplete reports a half-configured Environment pair.
var ErrAdminCredentialPairIncomplete = errors.New(
	"both " + config.AdminUsernameEnv + " and " + config.AdminPasswordEnv + " must be set in the container environment")

// insecureDefaultCredential is the legacy upstream default that must never stay
// usable in Mikrozia.
const insecureDefaultCredential = "admin"

// InspectAdminAccount reports where the administrator credential comes from
// without changing the stored account.
func (s *UserService) InspectAdminAccount() (AdminCredentialState, error) {
	username := config.GetAdminUsername()
	password := config.GetAdminPassword()
	hasUsername := username != ""
	hasPassword := strings.TrimSpace(password) != ""
	if hasUsername != hasPassword {
		return AdminCredentialState{}, ErrAdminCredentialPairIncomplete
	}

	user, err := s.GetFirstUser()
	missing := database.IsNotFound(err)
	if err != nil && !missing {
		return AdminCredentialState{}, err
	}

	if !hasUsername {
		switch {
		case missing:
			return AdminCredentialState{Source: AdminCredentialMissing}, nil
		case isInsecureDefaultCredential(user):
			return AdminCredentialState{Source: AdminCredentialLegacyDefault}, nil
		default:
			return AdminCredentialState{Source: AdminCredentialPreserved}, nil
		}
	}
	return AdminCredentialState{Source: AdminCredentialFromEnvironment, Managed: true}, nil
}

// ReconcileAdminAccount applies the container environment to the stored
// administrator account and reports where the credential comes from.
func (s *UserService) ReconcileAdminAccount() (AdminCredentialState, error) {
	username := config.GetAdminUsername()
	password := config.GetAdminPassword()
	hasUsername := username != ""
	hasPassword := strings.TrimSpace(password) != ""
	if hasUsername != hasPassword {
		return AdminCredentialState{}, ErrAdminCredentialPairIncomplete
	}

	state, err := s.InspectAdminAccount()
	if err != nil {
		return AdminCredentialState{}, err
	}

	user, err := s.GetFirstUser()
	missing := database.IsNotFound(err)
	if err != nil && !missing {
		return AdminCredentialState{}, err
	}

	if !hasUsername {
		return state, nil
	}

	if missing {
		hashed, err := crypto.HashPasswordAsBcrypt(password)
		if err != nil {
			return AdminCredentialState{}, err
		}
		if err := database.GetDB().Create(&model.User{Username: username, Password: hashed}).Error; err != nil {
			return AdminCredentialState{}, err
		}
		return AdminCredentialState{Source: AdminCredentialFromEnvironment, Managed: true, Changed: true}, nil
	}

	updates := map[string]any{}
	if user.Username != username {
		updates["username"] = username
	}
	if !crypto.CheckPasswordHash(user.Password, password) {
		hashed, err := crypto.HashPasswordAsBcrypt(password)
		if err != nil {
			return AdminCredentialState{}, err
		}
		updates["password"] = hashed
	}
	if len(updates) == 0 {
		return AdminCredentialState{Source: AdminCredentialFromEnvironment, Managed: true}, nil
	}
	updates["login_epoch"] = gorm.Expr("login_epoch + 1")
	if err := database.GetDB().Model(&model.User{}).Where("id = ?", user.Id).Updates(updates).Error; err != nil {
		return AdminCredentialState{}, err
	}
	return AdminCredentialState{Source: AdminCredentialFromEnvironment, Managed: true, Changed: true}, nil
}

// isInsecureDefaultCredential reports whether the stored account still holds the
// legacy default username/password pair.
func isInsecureDefaultCredential(user *model.User) bool {
	if user == nil || user.Username != insecureDefaultCredential {
		return false
	}
	return crypto.CheckPasswordHash(user.Password, insecureDefaultCredential)
}
