package service

import (
	"errors"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

// CheckFamilyRelay covers formats with the standard text billing lifecycle.
func CheckFamilyRelay(c *gin.Context, group string) *ChannelSelectError {
	if !common.IsFamilyQuotaGroup(group) {
		return nil
	}
	if common.GetContextKeyString(c, constant.ContextKeyUserGroup) != group {
		return &ChannelSelectError{StatusCode: http.StatusForbidden, Code: types.ErrorCodeAccessDenied, Message: model.ErrFamilyMemberRequired.Error()}
	}
	if c.Request.Method == http.MethodPost {
		switch c.Request.URL.Path {
		case "/v1/chat/completions", "/pg/chat/completions", "/v1/responses", "/v1/messages":
			return nil
		}
	}
	return &ChannelSelectError{StatusCode: http.StatusBadRequest, Code: types.ErrorCodeInvalidRequest, Message: "Family quota supports synchronous text requests only."}
}

type FamilyFunding struct {
	userID   int
	group    string
	holdID   int64
	reserved int
}

func (f *FamilyFunding) Source() string { return BillingSourceFamily }

func (f *FamilyFunding) PreConsume(amount int) error {
	if f.holdID == 0 {
		id, err := model.ReserveFamilyQuota(f.userID, f.group, amount)
		if err != nil {
			return err
		}
		f.holdID = id
	} else if err := model.AdjustFamilyHold(f.holdID, amount); err != nil {
		return err
	}
	f.reserved += amount
	return nil
}

func (f *FamilyFunding) Settle(delta int) error {
	return model.FinalizeFamilyHold(f.holdID, f.reserved+delta)
}

func (f *FamilyFunding) Refund() error {
	return refundWithRetry(func() error { return model.FinalizeFamilyHold(f.holdID, 0) })
}

func familyBillingError(err error) *types.NewAPIError {
	switch {
	case errors.Is(err, model.ErrFamilyQuotaInsufficient):
		return types.NewErrorWithStatusCode(err, types.ErrorCodeInsufficientUserQuota, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
	case errors.Is(err, model.ErrFamilyMemberRequired):
		return types.NewErrorWithStatusCode(err, types.ErrorCodeAccessDenied, http.StatusForbidden, types.ErrOptionWithSkipRetry())
	default:
		return types.NewError(errors.New("Family quota accounting failed."), types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
	}
}
