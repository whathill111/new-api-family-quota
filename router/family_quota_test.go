package router

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestFamilyQuotaDashboardRoutes(t *testing.T) {
	const group = "family-dashboard-test"
	t.Setenv("FAMILY_QUOTA_GROUP", group)
	setupRelayRouterTestDB(t)
	require.NoError(t, i18n.Init())
	require.NoError(t, model.DB.AutoMigrate(&model.UserSession{}, &model.Log{}, &model.FamilyBalance{}, &model.FamilyCode{}, &model.FamilyQuotaHold{}))
	for _, user := range []model.User{
		{Id: 21, Username: "family-root", AffCode: "root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, Group: "default"},
		{Id: 22, Username: "family-user", AffCode: "famu", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: group},
		{Id: 23, Username: "family-outsider", AffCode: "norm", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default"},
	} {
		require.NoError(t, model.DB.Create(&user).Error)
	}
	login := func(id int) string {
		t.Helper()
		session, err := service.CreateLoginSession(id, "password", "127.0.0.1", "family test")
		require.NoError(t, err)
		return session.AccessToken
	}
	root, member, outsider := login(21), login(22), login(23)
	engine := gin.New()
	SetApiRouter(engine)
	do := func(method, path, token, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, "/api/family-quota"+path, strings.NewReader(body))
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		require.Equal(t, status, recorder.Code, recorder.Body.String())
		return recorder
	}

	do(http.MethodGet, "", "", "", http.StatusUnauthorized)
	state := do(http.MethodGet, "", outsider, "", http.StatusOK)
	require.False(t, gjson.Get(state.Body.String(), "data.member").Bool())
	require.Empty(t, gjson.Get(state.Body.String(), "data.group").String())
	state = do(http.MethodGet, "", member, "", http.StatusOK)
	require.True(t, gjson.Get(state.Body.String(), "data.member").Bool())
	require.False(t, gjson.Get(state.Body.String(), "data.can_manage").Bool())
	state = do(http.MethodGet, "", root, "", http.StatusOK)
	require.True(t, gjson.Get(state.Body.String(), "data.can_manage").Bool())

	const grant = `{"user_id":22,"amount_usd":"2"}`
	do(http.MethodPost, "/admin/grant", member, grant, http.StatusForbidden)
	issued := do(http.MethodPost, "/admin/codes", root, `{"user_id":22,"amount_usd":"1","count":1,"valid_days":30}`, http.StatusOK)
	code := gjson.Get(issued.Body.String(), "data.codes.0").String()
	require.NotEmpty(t, code)
	require.Contains(t, issued.Header().Get("Cache-Control"), "no-store")
	redeem := `{"code":"` + code + `"}`
	do(http.MethodPost, "/redeem", outsider, redeem, http.StatusForbidden)
	do(http.MethodPost, "/redeem", member, redeem, http.StatusOK)
	do(http.MethodPost, "/redeem", member, redeem, http.StatusBadRequest)
	do(http.MethodPost, "/admin/grant", root, grant, http.StatusOK)
	state = do(http.MethodGet, "", member, "", http.StatusOK)
	require.Equal(t, int64(3*common.QuotaPerUnit), gjson.Get(state.Body.String(), "data.balance").Int())
	var logs []model.Log
	require.NoError(t, model.LOG_DB.Find(&logs).Error)
	for _, log := range logs {
		require.NotContains(t, log.Content, code)
		require.NotContains(t, log.Other, code)
	}
}

func TestFamilyQuotaChatRoutes(t *testing.T) {
	const group = "family-chat-test"
	t.Setenv("FAMILY_QUOTA_GROUP", group)
	h := newRelayHarness(t)
	require.NoError(t, model.DB.AutoMigrate(&model.FamilyBalance{}, &model.FamilyCode{}, &model.FamilyQuotaHold{}))
	ratios := ratio_setting.GetGroupRatioCopy()
	saved, err := common.Marshal(ratios)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(string(saved))) })
	ratios[group] = 0.05
	config, err := common.Marshal(ratios)
	require.NoError(t, err)
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(string(config)))

	channel := h.newChannel(constant.ChannelTypeOpenAI, "local-test-key")
	channel.Group = group
	h.install(t, channel)
	require.NoError(t, model.DB.Model(&model.Token{}).Where("id = ?", h.tokenID).Update("group", group).Error)
	const route = "/v1/chat/completions"

	outsider := h.post(t, route, relayTestBody)
	require.Equal(t, http.StatusForbidden, outsider.status, outsider.body)
	require.Empty(t, h.upstream.captured())
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", h.userID).Updates(model.User{Group: group}).Error)
	empty := h.post(t, route, relayTestBody)
	require.Equal(t, http.StatusForbidden, empty.status, empty.body)
	require.Empty(t, h.upstream.captured())
	require.Zero(t, empty.quota)

	const initial = 1000000
	require.NoError(t, model.GrantFamilyQuota(h.userID, group, initial))
	wantBalance := int64(initial)
	for _, body := range []string{relayTestBody, strings.Replace(relayTestBody, `"max_tokens"`, `"stream":true,"max_tokens"`, 1)} {
		outcome := h.post(t, route, body)
		require.Equal(t, http.StatusOK, outcome.status, outcome.body)
		require.Positive(t, outcome.quota)
		require.Equal(t, outcome.quota, outcome.log.Quota)
		require.Equal(t, "family", gjson.Get(outcome.log.Other, "billing_source").String())
		wantBalance -= int64(outcome.quota)
		balance, err := model.GetFamilyBalance(h.userID, group)
		require.NoError(t, err)
		require.Equal(t, wantBalance, balance)
	}
	require.Len(t, h.upstream.captured(), 2)

	h.upstream.respond = func(w http.ResponseWriter, _ int, _ capturedRequest) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, `{"error":{"message":"test upstream failure"}}`)
	}
	failed := h.post(t, route, relayTestBody)
	require.GreaterOrEqual(t, failed.status, http.StatusBadRequest, failed.body)
	require.Zero(t, failed.quota)
	balance, err := model.GetFamilyBalance(h.userID, group)
	require.NoError(t, err)
	require.Equal(t, wantBalance, balance)
	var row model.FamilyBalance
	require.NoError(t, model.DB.First(&row).Error)
	require.Zero(t, row.Held)
	wallet, err := model.GetUserQuota(h.userID, true)
	require.NoError(t, err)
	require.Equal(t, 100_000_000, wallet)
}
