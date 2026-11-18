package model

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrFamilyMemberRequired    = errors.New("Family group membership required.")
	ErrInvalidFamilyCode       = errors.New("Family code is invalid, expired, or already used.")
	ErrFamilyQuotaInsufficient = errors.New("Insufficient family activity balance.")
	ErrFamilyQuotaLimit        = errors.New("Family balance or amount exceeds the supported range.")
)

type FamilyBalance struct {
	UserID    int    `gorm:"primaryKey;autoIncrement:false"`
	GroupName string `gorm:"primaryKey;size:191"`
	Balance   int64  `gorm:"not null;default:0" json:"balance"`
	Held      int64  `gorm:"not null;default:0" json:"-"`
}

type FamilyCode struct {
	ID           int    `gorm:"primaryKey"`
	Digest       string `gorm:"size:64;uniqueIndex;not null"`
	GroupName    string `gorm:"size:191;not null"`
	Amount       int64  `gorm:"not null"`
	TargetUserID int    `gorm:"not null;default:0"`
	CreatedBy    int
	CreatedAt    int64
	ExpiresAt    int64
	RedeemedBy   int   `gorm:"not null;default:0"`
	RedeemedAt   int64 `gorm:"not null;default:0"`
}

// FamilyQuotaHold records reservations durably and permits idempotent finalization.
type FamilyQuotaHold struct {
	ID          int64  `gorm:"primaryKey"`
	UserID      int    `gorm:"not null"`
	GroupName   string `gorm:"size:191;not null"`
	Reserved    int64  `gorm:"not null"`
	Charged     int64  `gorm:"not null;default:0"`
	CreatedAt   int64
	FinalizedAt int64 `gorm:"not null;default:0;index"`
}

func RequireFamilyMember(tx *gorm.DB, userID int, group string) error {
	if userID <= 0 || !common.IsFamilyQuotaGroup(group) {
		return ErrFamilyMemberRequired
	}
	var user User
	err := lockForUpdate(tx).Select([]string{"id", "group", "status"}).First(&user, userID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) || err == nil && (user.Group != group || user.Status != common.UserStatusEnabled) {
		return ErrFamilyMemberRequired
	}
	return err
}

func GetFamilyBalance(userID int, group string) (int64, error) {
	var balance FamilyBalance
	err := DB.Where("user_id = ? AND group_name = ?", userID, group).First(&balance).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	return balance.Balance, err
}

func familyBalanceQuery(tx *gorm.DB, userID int, group string) *gorm.DB {
	return tx.Model(&FamilyBalance{}).Where("user_id = ? AND group_name = ?", userID, group)
}

func creditFamilyBalance(tx *gorm.DB, userID int, group string, amount int64) error {
	if amount <= 0 || amount > common.MaxWalletQuota {
		return ErrFamilyQuotaLimit
	}
	row := FamilyBalance{UserID: userID, GroupName: group}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return err
	}
	result := familyBalanceQuery(tx, userID, group).
		Where("balance + held <= ?", common.MaxWalletQuota-amount).
		UpdateColumn("balance", gorm.Expr("balance + ?", amount))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrFamilyQuotaLimit
	}
	return nil
}

func GrantFamilyQuota(userID int, group string, amount int64) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := RequireFamilyMember(tx, userID, group); err != nil {
			return err
		}
		return creditFamilyBalance(tx, userID, group, amount)
	})
}

func familyCodeDigest(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

func CreateFamilyCodes(group string, amount int64, count, targetUser, adminID int, expiresAt int64) ([]string, error) {
	now := common.GetTimestamp()
	if !common.IsFamilyQuotaGroup(group) || amount <= 0 || amount > common.MaxWalletQuota ||
		count < 1 || count > 100 || targetUser < 0 || (targetUser > 0 && count != 1) ||
		adminID <= 0 || expiresAt <= now || expiresAt > now+366*24*60*60 {
		return nil, ErrFamilyQuotaLimit
	}
	codes := make([]string, count)
	rows := make([]FamilyCode, count)
	for i := range count {
		codes[i] = "FAM_" + rand.Text()
		rows[i] = FamilyCode{Digest: familyCodeDigest(codes[i]), GroupName: group, Amount: amount,
			TargetUserID: targetUser, CreatedBy: adminID, CreatedAt: now, ExpiresAt: expiresAt}
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		if targetUser > 0 {
			if err := RequireFamilyMember(tx, targetUser, group); err != nil {
				return err
			}
		}
		return tx.Create(&rows).Error
	})
	if err != nil {
		return nil, err
	}
	return codes, nil
}

func RedeemFamilyCode(userID int, group, code string) (int64, error) {
	code = strings.TrimSpace(code)
	if len(code) < 30 || len(code) > 64 || !strings.HasPrefix(code, "FAM_") {
		return 0, ErrInvalidFamilyCode
	}
	var card FamilyCode
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := RequireFamilyMember(tx, userID, group); err != nil {
			return err
		}
		now := common.GetTimestamp()
		digest := familyCodeDigest(code)
		lookup := tx.Where("digest = ? AND group_name = ?", digest, group)
		if err := lookup.First(&card).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrInvalidFamilyCode
			}
			return err
		}
		result := tx.Model(&FamilyCode{}).
			Where("id = ? AND redeemed_by = 0 AND expires_at > ? AND (target_user_id = 0 OR target_user_id = ?)", card.ID, now, userID).
			UpdateColumns(map[string]any{"redeemed_by": userID, "redeemed_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrInvalidFamilyCode
		}
		return creditFamilyBalance(tx, userID, group, card.Amount)
	})
	if err != nil {
		return 0, err
	}
	return card.Amount, nil
}

func reserveFamilyFunds(tx *gorm.DB, userID int, group string, amount int64) error {
	result := familyBalanceQuery(tx, userID, group).Where("balance >= ?", amount).
		UpdateColumns(map[string]any{"balance": gorm.Expr("balance - ?", amount), "held": gorm.Expr("held + ?", amount)})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrFamilyQuotaInsufficient
	}
	return nil
}

func ReserveFamilyQuota(userID int, group string, amount int) (int64, error) {
	if amount <= 0 || amount > common.MaxQuota {
		return 0, ErrFamilyQuotaLimit
	}
	hold := FamilyQuotaHold{UserID: userID, GroupName: group, Reserved: int64(amount)}
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := RequireFamilyMember(tx, userID, group); err != nil {
			return err
		}
		if err := reserveFamilyFunds(tx, userID, group, int64(amount)); err != nil {
			return err
		}
		return tx.Create(&hold).Error
	})
	return hold.ID, err
}

// AdjustFamilyHold adds a reservation, or reverses a failed token adjustment.
func AdjustFamilyHold(id int64, delta int) error {
	if delta == 0 {
		return nil
	}
	if delta < -common.MaxQuota || delta > common.MaxQuota {
		return ErrFamilyQuotaLimit
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var hold FamilyQuotaHold
		if err := lockForUpdate(tx).First(&hold, id).Error; err != nil {
			return err
		}
		next := hold.Reserved + int64(delta)
		if hold.FinalizedAt != 0 || next <= 0 || next > common.MaxQuota {
			return ErrFamilyQuotaLimit
		}
		if delta > 0 {
			if err := reserveFamilyFunds(tx, hold.UserID, hold.GroupName, int64(delta)); err != nil {
				return err
			}
		} else {
			result := familyBalanceQuery(tx, hold.UserID, hold.GroupName).Where("held >= ?", -delta).
				UpdateColumns(map[string]any{"balance": gorm.Expr("balance - ?", delta), "held": gorm.Expr("held + ?", delta)})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrFamilyQuotaLimit
			}
		}
		update := tx.Model(&FamilyQuotaHold{}).Where("id = ? AND finalized_at = 0 AND reserved = ?", id, hold.Reserved).UpdateColumn("reserved", next)
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return ErrFamilyQuotaLimit
		}
		return nil
	})
}

// FinalizeFamilyHold settles the full charge, or releases the hold if actual is zero.
// A final charge above the estimate can overdraw this balance only.
func FinalizeFamilyHold(id int64, actual int) error {
	if actual < 0 || actual > common.MaxQuota {
		return ErrFamilyQuotaLimit
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var hold FamilyQuotaHold
		if err := lockForUpdate(tx).First(&hold, id).Error; err != nil {
			return err
		}
		if hold.FinalizedAt != 0 {
			if hold.Charged == int64(actual) {
				return nil
			}
			return ErrFamilyQuotaLimit
		}
		delta := hold.Reserved - int64(actual)
		result := familyBalanceQuery(tx, hold.UserID, hold.GroupName).
			Where("held >= ? AND balance >= ?", hold.Reserved, -common.MaxWalletQuota-delta).
			UpdateColumns(map[string]any{"balance": gorm.Expr("balance + ?", delta), "held": gorm.Expr("held - ?", hold.Reserved)})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrFamilyQuotaLimit
		}
		update := tx.Model(&FamilyQuotaHold{}).
			Where("id = ? AND finalized_at = 0 AND reserved = ?", id, hold.Reserved).
			UpdateColumns(map[string]any{"charged": actual, "finalized_at": common.GetTimestamp()})
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return ErrFamilyQuotaLimit
		}
		return nil
	})
}
