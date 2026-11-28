package traffic

// 自动保留策略：录制是全量明文落盘的，长时间运行的实例必须有人按年龄/容量
// 清理，否则磁盘只涨不降。这里提供两件事：
//
//   - Prune: 按「早于某时刻」与「目录总占用上限」删除最旧的流量；
//   - Usage: 数据目录占用快照（带短缓存，供监测页 5s 轮询读取）。
//
// 删除与 record() 共用 wmu，和既有 DeleteHost/DeleteAll 走同一套事务 +
// blob GC + 索引回收逻辑；不另造一套删除路径。

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// pruneBatchDefault 是单轮按容量清理删除的最旧条数。清理在后台定时跑，
	// 一次删太多会长时间占住 wmu，拖慢正在录制的请求。
	pruneBatchDefault = 5000
	// pruneMaxRounds 限制一次调用内按容量清理的轮数；没清到目标就留给下一轮，
	// 避免异常数据（例如删不掉的旧目录）让清理循环停不下来。
	pruneMaxRounds = 20
	// usageCacheTTL 让监测页的 5s 轮询不会把整棵目录树走一遍。
	usageCacheTTL = 60 * time.Second
)

// PruneOptions controls one automatic retention pass. Zero values mean "off",
// so an unconfigured instance keeps today's behaviour (nothing is deleted).
type PruneOptions struct {
	OlderThan time.Time // 删除该时刻之前的流量
	MaxBytes  int64     // 目录总占用上限；超出时从最旧开始删
	BatchSize int       // 容量清理单轮条数；<=0 用默认值
}

// Usage is one point-in-time snapshot of the traffic data directory.
type Usage struct {
	Bytes      int64 `json:"bytes"`
	Files      int64 `json:"files"`
	IndexBytes int64 `json:"index_bytes"`
	BlobBytes  int64 `json:"blob_bytes"`
}

// PruneResult reports what one retention pass did.
type PruneResult struct {
	Deleted int64
	Usage   Usage
}

// Prune deletes traffic older than opts.OlderThan and, when the directory still
// exceeds opts.MaxBytes, keeps deleting the oldest exchanges until it fits or
// the round budget is exhausted.
//
// Blob GC runs after each capacity round: blobs are content-addressed and shared,
// and leaving unreferenced ones on disk would make the usage measurement stay
// above the cap and delete more exchanges than necessary.
func (t *Traffic) Prune(opts PruneOptions) (PruneResult, error) {
	var res PruneResult
	if opts.OlderThan.IsZero() && opts.MaxBytes <= 0 {
		res.Usage = t.Usage()
		return res, nil
	}

	if !opts.OlderThan.IsZero() {
		n, err := t.pruneDelete(`ts < ?`, opts.OlderThan.Unix())
		res.Deleted += n
		if err != nil {
			res.Usage = t.Usage()
			return res, err
		}
	}

	if opts.MaxBytes > 0 {
		batch := opts.BatchSize
		if batch <= 0 {
			batch = pruneBatchDefault
		}
		for round := 0; round < pruneMaxRounds; round++ {
			u := t.measureUsage()
			if u.Bytes <= opts.MaxBytes {
				break
			}
			var cutoff int64
			err := t.db.QueryRow(
				`SELECT ts FROM exchanges ORDER BY ts ASC, id ASC LIMIT 1 OFFSET ?`,
				batch-1,
			).Scan(&cutoff)
			var n int64
			switch {
			case errors.Is(err, sql.ErrNoRows):
				// 剩余条数不足一批：全部删掉。
				n, err = t.pruneDelete(`1=1`)
			case err != nil:
				res.Usage = t.measureUsage()
				return res, err
			default:
				n, err = t.pruneDelete(`ts <= ?`, cutoff)
			}
			if err != nil {
				res.Usage = t.measureUsage()
				return res, err
			}
			res.Deleted += n
			if n == 0 {
				log.Printf("[traffic] 容量清理已无流量可删，目录仍超出上限 %d 字节（可能还有旧版宿主目录）", opts.MaxBytes)
				break
			}
			// 每轮先回收无引用 blob，再让下一轮测量看到真实占用。
			t.wmu.Lock()
			gcErr := t.gcBlobs()
			t.wmu.Unlock()
			t.invalidateUsage()
			if gcErr != nil {
				log.Printf("[traffic] 容量清理回收 blob 失败：%v", gcErr)
			}
		}
	}

	if res.Deleted > 0 {
		// 按年龄删除时不会走上面的容量循环，这里补一次 blob GC；容量路径已经
		// 每轮回收过，重复一次只是幂等的空扫。
		t.wmu.Lock()
		gcErr := t.gcBlobs()
		t.wmu.Unlock()
		if gcErr != nil {
			log.Printf("[traffic] 清理后回收 blob 失败：%v", gcErr)
		}
		// 索引空间回收是后台有界步骤；这里只负责把它踢起来。
		t.reclaim()
		t.invalidateUsage()
	}
	res.Usage = t.measureUsage()
	t.usageMu.Lock()
	t.usageCache = res.Usage
	t.usageAt = time.Now()
	t.usageMu.Unlock()
	return res, nil
}

// pruneDelete runs one delete transaction under the write lock, mirroring the
// host-scoped deletions but without staging legacy host trees: an age/size prune
// removes only part of a host, so removing the whole tree would lose newer rows'
// on-disk copies.
func (t *Traffic) pruneDelete(where string, args ...any) (int64, error) {
	t.wmu.Lock()
	defer t.wmu.Unlock()
	tx, err := t.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed
	n, err := t.deleteWhere(tx, where, args...)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("提交流量清理事务: %w", err)
	}
	return n, nil
}

// Usage returns the cached data-directory snapshot. The cache is refreshed by
// Prune and expires after usageCacheTTL.
func (t *Traffic) Usage() Usage {
	t.usageMu.Lock()
	defer t.usageMu.Unlock()
	if !t.usageAt.IsZero() && time.Since(t.usageAt) < usageCacheTTL {
		return t.usageCache
	}
	u := t.measureUsage()
	t.usageCache = u
	t.usageAt = time.Now()
	return u
}

// UsageNow bypasses the cache. Used by tests and by callers that need the value
// immediately after a deletion.
func (t *Traffic) UsageNow() Usage {
	return t.measureUsage()
}

func (t *Traffic) invalidateUsage() {
	t.usageMu.Lock()
	t.usageAt = time.Time{}
	t.usageMu.Unlock()
}

// measureUsage walks the data directory. It intentionally takes no write lock:
// it only reads file metadata, and holding wmu while walking millions of legacy
// files would stall request recording. The result is therefore approximate under
// concurrent writes, which is fine for a retention threshold.
func (t *Traffic) measureUsage() Usage {
	var u Usage
	blobRoot := filepath.Join(t.dir, "_blobs") + string(os.PathSeparator)
	_ = filepath.WalkDir(t.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() {
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil {
			return nil
		}
		u.Bytes += info.Size()
		u.Files++
		if strings.HasPrefix(p, blobRoot) {
			u.BlobBytes += info.Size()
		}
		return nil
	})
	u.IndexBytes = t.indexBytes()
	return u
}
