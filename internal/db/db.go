package db

import (
	_ "embed"
	"fmt"
	"os"
	"path"
	"time"

	"github.com/filswan/go-mcs-sdk/mcs/api/common/logs"
	"github.com/swanchain/computing-provider-v2/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const cpDBName = "provider.db"

var DB *gorm.DB

// InitDb initializes the database connection with retry logic
func InitDb(cpRepoPath string) {
	var err error
	maxRetries := 5
	baseDelay := time.Second

	dbPath := fmt.Sprintf("%s?journal_mode=wal&cache=shared&timeout=5s&busy_timeout=60000&synchronous=NORMAL", path.Join(cpRepoPath, cpDBName))

	for i := 0; i < maxRetries; i++ {
		DB, err = gorm.Open(sqlite.Open(dbPath), &gorm.Config{
			Logger: logger.New(myLog{}, logger.Config{
				SlowThreshold: 5 * time.Second,
				LogLevel:      logger.Error,
				Colorful:      false,
			}),
		})
		if err == nil {
			break
		}

		delay := baseDelay * time.Duration(1<<uint(i))
		logs.GetLogger().Warnf("Database connection failed (attempt %d/%d): %v, retrying in %v",
			i+1, maxRetries, err, delay)
		time.Sleep(delay)
	}

	if err != nil {
		panic(fmt.Sprintf("failed to connect database after %d attempts: %v", maxRetries, err))
	}

	DB.Exec("PRAGMA journal_mode=WAL;")
	DB.Exec("PRAGMA synchronous=NORMAL;")

	sqlDB, err := DB.DB()
	if err != nil {
		panic(fmt.Sprintf("failed to get underlying database connection: %v", err))
	}

	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(0)

	if err = DB.AutoMigrate(
		&models.CpInfoEntity{}); err != nil {
		panic(fmt.Sprintf("failed to auto migrate for provider db: %v", err))
	}

	logs.GetLogger().Info("Database initialized successfully")
}

func NewDbService() *gorm.DB {
	return DB
}

type myLog struct {
}

func (c myLog) Printf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stdout, format, args...)
}
