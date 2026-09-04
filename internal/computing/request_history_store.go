package computing

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/filswan/go-mcs-sdk/mcs/api/common/logs"
	"github.com/swanchain/computing-provider-v2/internal/db"
	"gorm.io/gorm"
)

// RequestHistoryEntity is one served request, kept so the Transactions view
// survives a restart.
//
// Until this existed the request list lived only in a 1000-entry in-memory
// ring, so every restart emptied it — and this agent restarts often enough
// that the earnings panel routinely reports counter resets. A filter and a
// pager over a list that only reaches back to the last restart are not much
// use.
type RequestHistoryEntity struct {
	ID        uint      `gorm:"primaryKey;autoIncrement"`
	RequestID string    `gorm:"index"`
	Model     string    `gorm:"index"`
	Source    string    `gorm:"index"`
	StartTime time.Time `gorm:"index;not null"`
	EndTime   time.Time
	LatencyMs float64
	TokensIn  int
	TokensOut int
	Streaming bool
	Success   bool
	// ErrorReason can be an upstream body, so it is truncated before storage:
	// a row per request means an unbounded field is an unbounded table.
	ErrorReason string
}

func (RequestHistoryEntity) TableName() string {
	return "request_history"
}

const (
	// maxErrorReasonBytes caps what one failure can cost on disk.
	maxErrorReasonBytes = 512
	// writeBatchSize is how many rows one insert carries. The database allows a
	// single open connection, so writes are batched rather than made per
	// request — otherwise a busy node serialises inference behind SQLite.
	writeBatchSize = 200
	// flushInterval bounds how stale the stored history can be. The dashboard
	// polls every 10s, so a second or two of lag is invisible.
	flushInterval = 2 * time.Second
	// queueSize is the burst absorbed before records are dropped. Dropping is
	// deliberate: recording history must never slow down or block serving.
	queueSize = 4096
)

// RequestStore persists request history and serves queries over it.
type RequestStore struct {
	queue   chan RequestMetric
	stop    chan struct{}
	done    chan struct{}
	mu      sync.Mutex
	running bool

	retentionDays int
	maxRows       int64

	// dropped counts records discarded because the queue was full, so the
	// condition is visible rather than silent.
	dropped atomic.Int64
}

// NewRequestStore builds a store with the given retention. A non-positive
// value falls back to the default.
func NewRequestStore(retentionDays int, maxRows int64) *RequestStore {
	if retentionDays <= 0 {
		retentionDays = 7
	}
	if maxRows <= 0 {
		maxRows = 200_000
	}
	return &RequestStore{
		queue:         make(chan RequestMetric, queueSize),
		stop:          make(chan struct{}),
		done:          make(chan struct{}),
		retentionDays: retentionDays,
		maxRows:       maxRows,
	}
}

// Start migrates the table and begins the writer and pruner.
func (s *RequestStore) Start() error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = true
	s.mu.Unlock()

	database := db.NewDbService()
	if database == nil {
		return nil
	}
	if err := database.AutoMigrate(&RequestHistoryEntity{}); err != nil {
		return err
	}

	go s.writeLoop()
	go s.pruneLoop()
	logs.GetLogger().Info("Request history store started")
	return nil
}

// Stop drains what is queued and shuts the writer down.
func (s *RequestStore) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	s.mu.Unlock()

	close(s.stop)
	<-s.done
	if dropped := s.dropped.Load(); dropped > 0 {
		logs.GetLogger().Warnf("Request history dropped %d record(s) under load", dropped)
	}
}

// Record queues one request. It never blocks: a full queue drops the record
// rather than making the inference path wait on storage.
func (s *RequestStore) Record(req RequestMetric) {
	if s == nil {
		return
	}
	if len(req.ErrorReason) > maxErrorReasonBytes {
		req.ErrorReason = req.ErrorReason[:maxErrorReasonBytes]
	}
	select {
	case s.queue <- req:
	default:
		s.dropped.Add(1)
	}
}

func (s *RequestStore) writeLoop() {
	defer close(s.done)
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	batch := make([]RequestHistoryEntity, 0, writeBatchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if database := db.NewDbService(); database != nil {
			if err := database.CreateInBatches(batch, writeBatchSize).Error; err != nil {
				logs.GetLogger().Warnf("Failed to persist request history: %v", err)
			}
		}
		batch = batch[:0]
	}

	for {
		select {
		case req := <-s.queue:
			batch = append(batch, entityFor(req))
			if len(batch) >= writeBatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-s.stop:
			// Drain whatever is queued so a clean shutdown does not discard
			// the requests served just before it.
			for {
				select {
				case req := <-s.queue:
					batch = append(batch, entityFor(req))
					if len(batch) >= writeBatchSize {
						flush()
					}
					continue
				default:
				}
				break
			}
			flush()
			return
		}
	}
}

func entityFor(req RequestMetric) RequestHistoryEntity {
	return RequestHistoryEntity{
		RequestID:   req.RequestID,
		Model:       req.Model,
		Source:      string(req.Source),
		StartTime:   req.StartTime,
		EndTime:     req.EndTime,
		LatencyMs:   req.LatencyMs,
		TokensIn:    req.TokensIn,
		TokensOut:   req.TokensOut,
		Streaming:   req.Streaming,
		Success:     req.Success,
		ErrorReason: req.ErrorReason,
	}
}

func (s *RequestStore) pruneLoop() {
	// Prune on start as well as on the timer, so a node restarted more often
	// than the interval still enforces its retention.
	s.prune()
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.prune()
		case <-s.stop:
			return
		}
	}
}

// prune enforces both limits: age, and a row cap so a burst cannot fill the
// disk before the age limit ever bites.
func (s *RequestStore) prune() {
	database := db.NewDbService()
	if database == nil {
		return
	}

	cutoff := time.Now().AddDate(0, 0, -s.retentionDays)
	if result := database.Where("start_time < ?", cutoff).Delete(&RequestHistoryEntity{}); result.Error != nil {
		logs.GetLogger().Warnf("Failed to prune request history by age: %v", result.Error)
	} else if result.RowsAffected > 0 {
		logs.GetLogger().Debugf("Pruned %d request history row(s) older than %d days", result.RowsAffected, s.retentionDays)
	}

	var count int64
	if err := database.Model(&RequestHistoryEntity{}).Count(&count).Error; err != nil {
		return
	}
	if count <= s.maxRows {
		return
	}
	// Delete the oldest rows above the cap, identified by a threshold id rather
	// than by offset: ids are monotonic, so this is one indexed delete instead
	// of a scan.
	var threshold uint
	if err := database.Model(&RequestHistoryEntity{}).
		Order("id desc").Offset(int(s.maxRows)).Limit(1).
		Pluck("id", &threshold).Error; err != nil || threshold == 0 {
		return
	}
	if result := database.Where("id <= ?", threshold).Delete(&RequestHistoryEntity{}); result.Error != nil {
		logs.GetLogger().Warnf("Failed to prune request history by row cap: %v", result.Error)
	} else if result.RowsAffected > 0 {
		logs.GetLogger().Debugf("Pruned %d request history row(s) over the %d row cap", result.RowsAffected, s.maxRows)
	}
}

// Query returns one page of stored history, newest first, with the total
// matching the filters.
func (s *RequestStore) Query(q RequestHistoryQuery) (RequestHistoryPage, error) {
	page := RequestHistoryPage{Requests: []RequestMetric{}, Limit: q.Limit, Offset: q.Offset}
	if page.Limit <= 0 {
		page.Limit = 100
	}
	if page.Offset < 0 {
		page.Offset = 0
	}

	database := db.NewDbService()
	if database == nil {
		return page, nil
	}

	build := func() *gorm.DB {
		tx := database.Model(&RequestHistoryEntity{})
		if q.Model != "" {
			tx = tx.Where("model = ?", q.Model)
		}
		if q.Source != "" {
			if q.Source == string(SourceHub) {
				// Rows written before the source field existed carry none, and
				// every one of them arrived over the WebSocket. Excluding them
				// would make an operator's older history vanish the moment
				// they filter to Hub.
				tx = tx.Where("source = ? OR source = ''", q.Source)
			} else {
				tx = tx.Where("source = ?", q.Source)
			}
		}
		return tx
	}

	var total int64
	if err := build().Count(&total).Error; err != nil {
		return page, err
	}
	page.Total = int(total)

	var rows []RequestHistoryEntity
	if err := build().
		Order("start_time desc, id desc").
		Offset(page.Offset).Limit(page.Limit).
		Find(&rows).Error; err != nil {
		return page, err
	}
	for _, row := range rows {
		page.Requests = append(page.Requests, RequestMetric{
			RequestID:   row.RequestID,
			Model:       row.Model,
			StartTime:   row.StartTime,
			EndTime:     row.EndTime,
			LatencyMs:   row.LatencyMs,
			TokensIn:    row.TokensIn,
			TokensOut:   row.TokensOut,
			Streaming:   row.Streaming,
			Success:     row.Success,
			ErrorReason: row.ErrorReason,
			Source:      RequestSource(row.Source),
		})
	}
	return page, nil
}

// Dropped reports how many records were discarded because the queue was full.
func (s *RequestStore) Dropped() int64 {
	if s == nil {
		return 0
	}
	return s.dropped.Load()
}
