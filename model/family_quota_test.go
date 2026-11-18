package model

import (
	"errors"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestFamilyQuotaDatabaseMatrix(t *testing.T) {
	const group = "家族活动专属分组"
	t.Setenv("FAMILY_QUOTA_GROUP", group)
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			useFamilyQuotaTestDB(t, dialect)
			for _, user := range []User{
				{Id: 101, Username: "family_one", AffCode: "fam1", Group: group, Quota: 50000, Status: common.UserStatusEnabled},
				{Id: 102, Username: "family_two", AffCode: "fam2", Group: group, Quota: 50000, Status: common.UserStatusEnabled},
				{Id: 103, Username: "ordinary", AffCode: "norm", Group: "default", Quota: 50000, Status: common.UserStatusEnabled},
			} {
				require.NoError(t, DB.Create(&user).Error)
			}
			balance := func(userID int, want int64) {
				t.Helper()
				got, err := GetFamilyBalance(userID, group)
				require.NoError(t, err)
				require.Equal(t, want, got)
			}

			require.ErrorIs(t, GrantFamilyQuota(103, group, 100), ErrFamilyMemberRequired)
			_, err := ReserveFamilyQuota(101, group, 100)
			require.ErrorIs(t, err, ErrFamilyQuotaInsufficient)

			codes, err := CreateFamilyCodes(group, 100, 1, 101, 1, common.GetTimestamp()+3600)
			require.NoError(t, err)
			require.NotEqual(t, codes[0], familyCodeDigest(codes[0]))
			_, err = RedeemFamilyCode(103, group, codes[0])
			require.ErrorIs(t, err, ErrFamilyMemberRequired)
			_, err = RedeemFamilyCode(102, group, codes[0])
			require.ErrorIs(t, err, ErrInvalidFamilyCode)
			credited, err := RedeemFamilyCode(101, group, codes[0])
			require.NoError(t, err)
			require.EqualValues(t, 100, credited)
			_, err = RedeemFamilyCode(101, group, codes[0])
			require.ErrorIs(t, err, ErrInvalidFamilyCode)
			balance(101, 100)
			balance(102, 0)
			require.ErrorIs(t, GrantFamilyQuota(101, group, common.MaxWalletQuota), ErrFamilyQuotaLimit)

			codes, err = CreateFamilyCodes(group, 50, 2, 0, 1, common.GetTimestamp()+3600)
			require.NoError(t, err)
			require.NoError(t, DB.Model(&FamilyCode{}).Where("digest = ?", familyCodeDigest(codes[1])).
				Update("expires_at", common.GetTimestamp()-1).Error)
			_, err = RedeemFamilyCode(102, group, codes[1])
			require.ErrorIs(t, err, ErrInvalidFamilyCode)
			results := make(chan error, 2)
			for _, id := range []int{101, 102} {
				go func() {
					_, err := RedeemFamilyCode(id, group, codes[0])
					results <- err
				}()
			}
			successes := 0
			for range 2 {
				if err := <-results; err == nil {
					successes++
				} else {
					require.True(t, errors.Is(err, ErrInvalidFamilyCode), "%v", err)
				}
			}
			require.Equal(t, 1, successes)
			first, err := GetFamilyBalance(101, group)
			require.NoError(t, err)
			balance(102, 150-first)

			hold, err := ReserveFamilyQuota(101, group, 60)
			require.NoError(t, err)
			require.ErrorIs(t, AdjustFamilyHold(hold, 100), ErrFamilyQuotaInsufficient)
			require.NoError(t, AdjustFamilyHold(hold, 25))
			require.NoError(t, AdjustFamilyHold(hold, -10))
			require.NoError(t, FinalizeFamilyHold(hold, 75))
			require.NoError(t, FinalizeFamilyHold(hold, 75))
			require.ErrorIs(t, FinalizeFamilyHold(hold, 0), ErrFamilyQuotaLimit)
			balance(101, first-75)

			hold, err = ReserveFamilyQuota(101, group, 10)
			require.NoError(t, err)
			require.NoError(t, FinalizeFamilyHold(hold, 0))
			require.NoError(t, FinalizeFamilyHold(hold, 0))
			balance(101, first-75)

			hold, err = ReserveFamilyQuota(101, group, 1)
			require.NoError(t, err)
			require.NoError(t, FinalizeFamilyHold(hold, int(first-65)))
			balance(101, -10)
			_, err = ReserveFamilyQuota(101, group, 1)
			require.ErrorIs(t, err, ErrFamilyQuotaInsufficient)
			require.NoError(t, GrantFamilyQuota(101, group, 20))
			balance(101, 10)
			require.NoError(t, DB.Model(&User{}).Where("id = ?", 101).
				Updates(User{Group: "default"}).Error)
			_, err = ReserveFamilyQuota(101, group, 1)
			require.ErrorIs(t, err, ErrFamilyMemberRequired)
			var users []User
			require.NoError(t, DB.Find(&users).Error)
			for _, user := range users {
				require.Equal(t, 50000, user.Quota)
			}
		})
	}
}

func useFamilyQuotaTestDB(t *testing.T, dialect string) {
	t.Helper()
	dialector := sqlite.Open(":memory:")
	databaseType := common.DatabaseTypeSQLite
	switch dialect {
	case "mysql":
		dsn := os.Getenv("TEST_MYSQL_DSN")
		if dsn == "" {
			t.Skip("TEST_MYSQL_DSN is not configured")
		}
		dialector, databaseType = mysql.Open(dsn), common.DatabaseTypeMySQL
	case "postgres":
		dsn := os.Getenv("TEST_POSTGRES_DSN")
		if dsn == "" {
			t.Skip("TEST_POSTGRES_DSN is not configured")
		}
		dialector, databaseType = postgres.Open(dsn), common.DatabaseTypePostgreSQL
	}
	db, err := gorm.Open(dialector, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: "family_quota_test_"}})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	if dialect == "sqlite" {
		sqlDB.SetMaxOpenConns(1)
	} else {
		sqlDB.SetMaxOpenConns(4)
	}
	previousDB := DB
	mainType, logType := common.MainDatabaseType(), common.LogDatabaseType()
	DB = db
	common.SetDatabaseTypes(databaseType, databaseType)
	t.Cleanup(func() {
		DB = previousDB
		common.SetDatabaseTypes(mainType, logType)
		require.NoError(t, db.Migrator().DropTable(&FamilyCode{}, &FamilyQuotaHold{}, &FamilyBalance{}, &User{}))
		require.NoError(t, sqlDB.Close())
	})
	require.NoError(t, db.AutoMigrate(&User{}, &FamilyBalance{}, &FamilyCode{}, &FamilyQuotaHold{}))
}
