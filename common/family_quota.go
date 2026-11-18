package common

import (
	"os"
	"strings"
)

// FamilyQuotaGroup selects the existing activity group that uses its own wallet.
func FamilyQuotaGroup() string {
	group := strings.TrimSpace(os.Getenv("FAMILY_QUOTA_GROUP"))
	if group == "default" || group == "auto" || strings.Contains(group, ",") || len(group) > 191 {
		return ""
	}
	return group
}

func IsFamilyQuotaGroup(group string) bool {
	return group != "" && group == FamilyQuotaGroup()
}
