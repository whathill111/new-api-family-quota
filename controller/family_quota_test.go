package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestFamilyQuotaAmountValidation(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"amount_usd":"1.25"}`))
	var body struct {
		familyQuotaAmount
	}
	require.NoError(t, c.ShouldBindJSON(&body))
	got, err := body.quota()
	require.NoError(t, err)
	require.Equal(t, int64(1.25*common.QuotaPerUnit), got)
	for _, input := range []string{"0", "-1", "1.2345678", "1e1000000000", "0.000000", "9999999999999999999"} {
		t.Run(input, func(t *testing.T) {
			_, err := (familyQuotaAmount{Amount: input}).quota()
			require.ErrorIs(t, err, model.ErrFamilyQuotaLimit)
		})
	}
}
