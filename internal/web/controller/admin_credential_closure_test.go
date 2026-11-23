package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/echodev7/mikrozia/v3/internal/config"
	"github.com/echodev7/mikrozia/v3/internal/database"
	"github.com/echodev7/mikrozia/v3/internal/database/dbtest"
	"github.com/echodev7/mikrozia/v3/internal/database/model"
)

func TestContainerLegacyAccountRefusesCredentialMutationWithoutEnvironment(t *testing.T) {
	t.Setenv("XUI_IN_DOCKER", "true")
	t.Setenv(config.AdminUsernameEnv, "")
	t.Setenv(config.AdminPasswordEnv, "")
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	var before model.User
	if err := database.GetDB().First(&before).Error; err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	controller := &SettingController{}
	engine.POST("/update", controller.updateUser)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/update", nil))
	var body struct {
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Success {
		t.Fatal("legacy container account accepted competing credential mutation")
	}
	var after model.User
	if err := database.GetDB().First(&after).Error; err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("refused credential request changed the account")
	}
}
