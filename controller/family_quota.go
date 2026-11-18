package controller

import (
	"errors"
	"net/http"
	"regexp"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

type familyQuotaAmount struct {
	Amount string `json:"amount_usd" binding:"required,max=32"`
}

var familyQuotaDecimal = regexp.MustCompile(`^[0-9]+(\.[0-9]{1,6})?$`)

func (a familyQuotaAmount) quota() (int64, error) {
	if !familyQuotaDecimal.MatchString(a.Amount) {
		return 0, model.ErrFamilyQuotaLimit
	}
	amount, err := decimal.NewFromString(a.Amount)
	if err != nil || amount.Sign() <= 0 || amount.GreaterThan(decimal.NewFromInt(common.MaxWalletQuota)) {
		return 0, model.ErrFamilyQuotaLimit
	}
	quota, err := common.WalletQuotaFromDecimalStrict(amount.Mul(decimal.NewFromFloat(common.QuotaPerUnit)))
	if err != nil || quota <= 0 {
		return 0, model.ErrFamilyQuotaLimit
	}
	return int64(quota), nil
}

func GetFamilyQuota(c *gin.Context) {
	group := common.FamilyQuotaGroup()
	state := gin.H{"member": false, "can_manage": false, "balance": 0, "group": ""}
	if group == "" {
		common.ApiSuccess(c, state)
		return
	}
	state["can_manage"] = c.GetInt("role") == common.RoleRootUser
	err := model.RequireFamilyMember(model.DB, c.GetInt("id"), group)
	switch {
	case err == nil:
		balance, err := model.GetFamilyBalance(c.GetInt("id"), group)
		if err != nil {
			writeFamilyError(c, err)
			return
		}
		state["member"] = true
		state["balance"] = balance
	case !errors.Is(err, model.ErrFamilyMemberRequired):
		writeFamilyError(c, err)
		return
	}
	if state["member"] == true || state["can_manage"] == true {
		state["group"] = group
	}
	common.ApiSuccess(c, state)
}

func RedeemFamilyQuota(c *gin.Context) {
	var body struct {
		Code string `json:"code" binding:"required,max=64"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		writeFamilyError(c, model.ErrInvalidFamilyCode)
		return
	}
	group := common.FamilyQuotaGroup()
	userID := c.GetInt("id")
	credited, err := model.RedeemFamilyCode(userID, group, body.Code)
	if err != nil {
		writeFamilyError(c, err)
		return
	}
	model.RecordLog(userID, model.LogTypeTopup, common.NewMessage("family_quota.redeemed", map[string]any{"Quota": credited}))
	common.ApiSuccess(c, gin.H{"credited": credited})
}

func AdminGrantFamilyQuota(c *gin.Context) {
	var body struct {
		familyQuotaAmount
		UserID int `json:"user_id" binding:"required,gt=0"`
	}
	if c.ShouldBindJSON(&body) != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	amount, err := body.quota()
	if err != nil {
		writeFamilyError(c, err)
		return
	}
	group := common.FamilyQuotaGroup()
	if err := model.GrantFamilyQuota(body.UserID, group, amount); err != nil {
		writeFamilyError(c, err)
		return
	}
	recordManageAuditFor(c, body.UserID, "family_quota.grant", map[string]any{"group": group, "quota": amount})
	common.ApiSuccess(c, gin.H{"credited": amount})
}

func AdminCreateFamilyCodes(c *gin.Context) {
	var body struct {
		familyQuotaAmount
		Count     int `json:"count" binding:"required,min=1,max=100"`
		ValidDays int `json:"valid_days" binding:"required,min=1,max=365"`
		UserID    int `json:"user_id" binding:"gte=0"`
	}
	if c.ShouldBindJSON(&body) != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	amount, err := body.quota()
	if err != nil {
		writeFamilyError(c, err)
		return
	}
	group := common.FamilyQuotaGroup()
	expires := common.GetTimestamp() + int64(body.ValidDays)*86400
	codes, err := model.CreateFamilyCodes(group, amount, body.Count, body.UserID, c.GetInt("id"), expires)
	if err != nil {
		writeFamilyError(c, err)
		return
	}
	recordManageAudit(c, "family_quota.codes_create", map[string]any{
		"group": group, "count": len(codes), "quota": amount, "target_user_id": body.UserID, "expires_at": expires,
	})
	common.ApiSuccess(c, gin.H{"codes": codes, "expires_at": expires})
}

func writeFamilyError(c *gin.Context, err error) {
	status := http.StatusBadRequest
	message := "family_quota.accounting_failed"
	switch {
	case errors.Is(err, model.ErrFamilyMemberRequired):
		status = http.StatusForbidden
		message = "family_quota.member_required"
	case errors.Is(err, model.ErrInvalidFamilyCode):
		message = "family_quota.invalid_code"
	case errors.Is(err, model.ErrFamilyQuotaLimit):
		message = "family_quota.amount_limit"
	default:
		common.SysError(common.LogText("family quota request failed: %s", err.Error()))
		status = http.StatusInternalServerError
	}
	c.JSON(status, gin.H{"success": false, "message": i18n.T(c, message)})
}
