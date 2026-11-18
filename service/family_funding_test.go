package service

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestFamilyQuotaFundingLifecycle(t *testing.T) {
	const group = "family-funding-test"
	t.Setenv("FAMILY_QUOTA_GROUP", group)
	truncate(t)
	tables := []any{&model.FamilyBalance{}, &model.FamilyCode{}, &model.FamilyQuotaHold{}}
	require.NoError(t, model.DB.AutoMigrate(tables...))
	t.Cleanup(func() { require.NoError(t, model.DB.Migrator().DropTable(tables...)) })
	seedUser(t, 91, 10000)
	seedToken(t, 91, 91, "test-family-key", 1000)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", 91).Updates(model.User{Group: group}).Error)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := func(usingGroup string) *relaycommon.RelayInfo {
		return &relaycommon.RelayInfo{
			UsingGroup:  usingGroup,
			UserId:      91,
			TokenId:     91,
			TokenKey:    "test-family-key",
			UserSetting: dto.UserSetting{BillingPreference: "wallet_only"},
		}
	}
	familyBalance := func() int64 {
		t.Helper()
		balance, err := model.GetFamilyBalance(91, group)
		require.NoError(t, err)
		return balance
	}
	walletBalance := func() int {
		t.Helper()
		balance, err := model.GetUserQuota(91, true)
		require.NoError(t, err)
		return balance
	}
	tokenBalance := func() int {
		t.Helper()
		var token model.Token
		require.NoError(t, model.DB.First(&token, 91).Error)
		return token.RemainQuota
	}

	_, apiErr := NewBillingSession(c, info(group), 100)
	require.NotNil(t, apiErr)
	require.Equal(t, http.StatusForbidden, apiErr.StatusCode)
	require.Equal(t, 1000, tokenBalance())
	require.Equal(t, 10000, walletBalance())
	require.NoError(t, model.GrantFamilyQuota(91, group, 1000))

	family := info(group)
	require.Nil(t, PreConsumeBilling(c, 100, family))
	require.Equal(t, BillingSourceFamily, family.BillingSource)
	require.EqualValues(t, 900, familyBalance())
	require.NoError(t, SettleBilling(c, family, 100))
	require.NoError(t, SettleBilling(c, family, 100))
	require.EqualValues(t, 900, familyBalance())
	require.Equal(t, 900, tokenBalance())
	require.Equal(t, 10000, walletBalance())
	var row model.FamilyBalance
	require.NoError(t, model.DB.First(&row).Error)
	require.Zero(t, row.Held)

	failed := info(group)
	require.Nil(t, PreConsumeBilling(c, 100, failed))
	require.EqualValues(t, 800, familyBalance())
	failed.Billing.Refund(c)
	failed.Billing.Refund(c)
	require.Eventually(t, func() bool {
		balance, err := model.GetFamilyBalance(91, group)
		var token model.Token
		tokenErr := model.DB.First(&token, 91).Error
		return err == nil && balance == 900 && tokenErr == nil && token.RemainQuota == 900
	}, 3*time.Second, 10*time.Millisecond)
	require.Equal(t, 10000, walletBalance())
	require.Error(t, PostConsumeQuota(family, 150, 0, false))

	ordinary := info("default")
	session, apiErr := NewBillingSession(c, ordinary, 200)
	require.Nil(t, apiErr)
	require.Equal(t, BillingSourceWallet, ordinary.BillingSource)
	require.NoError(t, session.Settle(250))
	require.Equal(t, 9750, walletBalance())
	require.EqualValues(t, 900, familyBalance())
}

func TestFamilyQuotaRequestRouting(t *testing.T) {
	const group = "family-route-test"
	t.Setenv("FAMILY_QUOTA_GROUP", group)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
	require.Equal(t, http.StatusForbidden, CheckFamilyRelay(c, group).StatusCode)
	require.Nil(t, CheckFamilyRelay(c, "default"))
	common.SetContextKey(c, constant.ContextKeyUserGroup, group)
	require.Nil(t, CheckFamilyRelay(c, group))
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	require.Equal(t, http.StatusBadRequest, CheckFamilyRelay(c, group).StatusCode)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	require.Equal(t, http.StatusBadRequest, CheckFamilyRelay(c, group).StatusCode)
	require.NotContains(t, FilterUserTokenAutoGroups(group, []string{group, "default"}), group)
}
