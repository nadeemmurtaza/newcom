package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Phase 28/50: Advanced Custom Log-Structured Merge-tree (LSM-Tree) Search Engine
// MODULE 2: Low-Level LSM-Tree Storage Engine with Bloom Filters

var GlobalLSM *LSMTree

const MemTableFlushThreshold = 1000

// --- Bloom Filter Implementation (From Scratch) ---

type BloomFilter struct {
	bitset []uint64
	size   uint64
}

func NewBloomFilter(expectedElements int, falsePositiveRate float64) *BloomFilter {
	// m = - (n * ln(p)) / (ln(2)^2)
	// For simplicity, fixing size
	size := uint64(expectedElements * 10)
	return &BloomFilter{
		bitset: make([]uint64, (size/64)+1),
		size:   size,
	}
}

// Hash 1: FNV-1a
func fnv1a(data string) uint64 {
	var hash uint64 = 14695981039346656037
	for i := 0; i < len(data); i++ {
		hash ^= uint64(data[i])
		hash *= 1099511628211
	}
	return hash
}

// Hash 2: MurmurHash3 (Simplified 64-bit)
func murmur3(data string) uint64 {
	var hash uint64 = 525201411107845655
	for i := 0; i < len(data); i++ {
		hash ^= uint64(data[i])
		hash *= 0x5bd1e9955bd1e995
		hash ^= hash >> 47
	}
	return hash
}

// Hash 3: DJB2
func djb2(data string) uint64 {
	var hash uint64 = 5381
	for i := 0; i < len(data); i++ {
		hash = ((hash << 5) + hash) + uint64(data[i])
	}
	return hash
}

func (b *BloomFilter) Add(key string) {
	h1 := fnv1a(key) % b.size
	h2 := murmur3(key) % b.size
	h3 := djb2(key) % b.size

	b.bitset[h1/64] |= (1 << (h1 % 64))
	b.bitset[h2/64] |= (1 << (h2 % 64))
	b.bitset[h3/64] |= (1 << (h3 % 64))
}

func (b *BloomFilter) MightContain(key string) bool {
	h1 := fnv1a(key) % b.size
	if (b.bitset[h1/64] & (1 << (h1 % 64))) == 0 {
		return false
	}
	h2 := murmur3(key) % b.size
	if (b.bitset[h2/64] & (1 << (h2 % 64))) == 0 {
		return false
	}
	h3 := djb2(key) % b.size
	if (b.bitset[h3/64] & (1 << (h3 % 64))) == 0 {
		return false
	}
	return true
}

// --- LSM Tree Core ---

type Record struct {
	MsgIDs    []string
	Tombstone bool
}

type LSMTree struct {
	mu          sync.RWMutex
	memTable    map[string]Record
	tokenCount  int
	walFile     *os.File
	ssTableDir  string
	ssTableID   int
	bloomCaches map[int]*BloomFilter
}

func NewLSMTree(dir string) *LSMTree {
	os.MkdirAll(dir, 0755)
	wal, err := os.OpenFile(filepath.Join(dir, "wal.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatalf("Failed to open WAL: %v", err)
	}

	lsm := &LSMTree{
		memTable:    make(map[string]Record),
		walFile:     wal,
		ssTableDir:  dir,
		ssTableID:   0,
		bloomCaches: make(map[int]*BloomFilter),
	}

	// Recovery would go here (replay WAL into MemTable)

	go lsm.CompactionWorker()
	return lsm
}

func (lsm *LSMTree) tokenize(text string) []string {
	words := strings.Fields(strings.ToLower(text))
	set := make(map[string]bool)
	var tokens []string
	for _, w := range words {
		if !set[w] {
			set[w] = true
			tokens = append(tokens, w)
		}
	}
	return tokens
}

// Delete marks a token with a tombstone
func (lsm *LSMTree) DeleteToken(token string) {
	lsm.mu.Lock()
	defer lsm.mu.Unlock()

	lsm.walFile.WriteString(fmt.Sprintf("DEL|%s\n", token))
	lsm.walFile.Sync()

	lsm.memTable[token] = Record{Tombstone: true}
	lsm.tokenCount++

	if lsm.tokenCount >= MemTableFlushThreshold {
		lsm.flushMemTableToSSTable()
	}
}

func (lsm *LSMTree) IndexEmail(msgID, text string) {
	tokens := lsm.tokenize(text)
	
	lsm.mu.Lock()
	defer lsm.mu.Unlock()

	logEntry := fmt.Sprintf("PUT|%s|%s\n", msgID, strings.Join(tokens, ","))
	lsm.walFile.WriteString(logEntry)
	lsm.walFile.Sync()

	for _, token := range tokens {
		rec := lsm.memTable[token]
		if rec.Tombstone {
			rec.MsgIDs = []string{}
			rec.Tombstone = false
		}
		rec.MsgIDs = append(rec.MsgIDs, msgID)
		lsm.memTable[token] = rec
		lsm.tokenCount++
	}

	if lsm.tokenCount >= MemTableFlushThreshold {
		lsm.flushMemTableToSSTable()
	}
}

func (lsm *LSMTree) flushMemTableToSSTable() {
	if lsm.tokenCount == 0 { return }
	
	lsm.ssTableID++
	sstablePath := filepath.Join(lsm.ssTableDir, fmt.Sprintf("sstable_0_%d.sst", lsm.ssTableID))
	
	var keys []string
	for k := range lsm.memTable {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	file, err := os.Create(sstablePath)
	if err != nil {
		log.Printf("LSM Error creating SSTable: %v", err)
		return
	}
	defer file.Close()

	bloom := NewBloomFilter(len(keys), 0.01)

	for _, k := range keys {
		bloom.Add(k)
		rec := lsm.memTable[k]
		jsonBytes, _ := json.Marshal(rec)
		file.WriteString(fmt.Sprintf("%s:%s\n", k, string(jsonBytes)))
	}

	lsm.bloomCaches[lsm.ssTableID] = bloom
	log.Printf("LSM Flush: Wrote SSTable %d with %d keys", lsm.ssTableID, len(keys))

	lsm.memTable = make(map[string]Record)
	lsm.tokenCount = 0
	lsm.walFile.Truncate(0)
	lsm.walFile.Seek(0, 0)
}

// --- Compaction (Merge Sort of SSTables) ---

func (lsm *LSMTree) CompactionWorker() {
	for {
		time.Sleep(1 * time.Minute)
		lsm.mu.Lock()
		// Find all Level 0 files
		files, err := filepath.Glob(filepath.Join(lsm.ssTableDir, "sstable_0_*.sst"))
		if err != nil || len(files) < 2 {
			lsm.mu.Unlock()
			continue
		}

		// Sort files numerically by the X in sstable_0_X.sst
		sort.Slice(files, func(i, j int) bool {
			var numI, numJ int
			fmt.Sscanf(filepath.Base(files[i]), "sstable_0_%d.sst", &numI)
			fmt.Sscanf(filepath.Base(files[j]), "sstable_0_%d.sst", &numJ)
			return numI < numJ
		})

		log.Printf("LSM Compaction: Merging %d files into Level 1", len(files))
		
		// Take the oldest two files to merge
		file1 := files[0]
		file2 := files[1]
		lsm.mu.Unlock()

		err = lsm.mergeTwoSSTables(file1, file2)
		if err != nil {
			log.Printf("LSM Compaction error: %v", err)
		}
	}
}

func (lsm *LSMTree) mergeTwoSSTables(path1, path2 string) error {
	f1, err := os.Open(path1)
	if err != nil { return err }
	defer f1.Close()

	f2, err := os.Open(path2)
	if err != nil { return err }
	defer f2.Close()

	outPath := filepath.Join(lsm.ssTableDir, fmt.Sprintf("sstable_1_%d.sst", time.Now().Unix()))
	out, err := os.Create(outPath)
	if err != nil { return err }
	defer out.Close()

	scan1 := bufio.NewScanner(f1)
	scan2 := bufio.NewScanner(f2)

	has1 := scan1.Scan()
	has2 := scan2.Scan()

	for has1 || has2 {
		var key1, key2 string
		var rec1, rec2 Record
		var line1, line2 string

		if has1 {
			line1 = scan1.Text()
			parts := strings.SplitN(line1, ":", 2)
			if len(parts) == 2 {
				key1 = parts[0]
				json.Unmarshal([]byte(parts[1]), &rec1)
			}
		}
		if has2 {
			line2 = scan2.Text()
			parts := strings.SplitN(line2, ":", 2)
			if len(parts) == 2 {
				key2 = parts[0]
				json.Unmarshal([]byte(parts[1]), &rec2)
			}
		}

		var chosenKey string
		var chosenRec Record

		if has1 && has2 {
			if key1 < key2 {
				chosenKey = key1
				chosenRec = rec1
				has1 = scan1.Scan()
			} else if key2 < key1 {
				chosenKey = key2
				chosenRec = rec2
				has2 = scan2.Scan()
			} else {
				// Keys match, merge records. Newer file (path2 typically) overrides.
				// For lists, we union them. If tombstone, it wipes previous.
				chosenKey = key1
				if rec2.Tombstone {
					chosenRec = rec2
				} else {
					chosenRec = Record{
						MsgIDs: append(rec1.MsgIDs, rec2.MsgIDs...),
						Tombstone: false,
					}
				}
				has1 = scan1.Scan()
				has2 = scan2.Scan()
			}
		} else if has1 {
			chosenKey = key1
			chosenRec = rec1
			has1 = scan1.Scan()
		} else if has2 {
			chosenKey = key2
			chosenRec = rec2
			has2 = scan2.Scan()
		}

		// Keep tombstones during L0 -> L1 merge because L1 files are never compacted against each other yet.
		// If we drop tombstones here, older L1 files could resurrect the deleted records.
		if chosenKey != "" {
			// Deduplicate MsgIDs
			set := make(map[string]bool)
			var dedup []string
			for _, mid := range chosenRec.MsgIDs {
				if !set[mid] {
					set[mid] = true
					dedup = append(dedup, mid)
				}
			}
			chosenRec.MsgIDs = dedup

			jsonBytes, _ := json.Marshal(chosenRec)
			out.WriteString(fmt.Sprintf("%s:%s\n", chosenKey, string(jsonBytes)))
		}
	}

	// Remove old Level 0 files
	os.Remove(path1)
	os.Remove(path2)
	log.Printf("LSM Compaction: Created %s, removed old SSTables", outPath)
	return nil
}

func (lsm *LSMTree) Search(term string) []string {
	term = strings.ToLower(term)
	lsm.mu.RLock()
	defer lsm.mu.RUnlock()

	var results []string

	if rec, exists := lsm.memTable[term]; exists {
		if rec.Tombstone {
			return []string{} // Deleted
		}
		results = append(results, rec.MsgIDs...)
	}

	// Check disk SSTables
	for i := lsm.ssTableID; i > 0; i-- {
		bloom, exists := lsm.bloomCaches[i]
		if exists && !bloom.MightContain(term) {
			continue
		}

		sstablePath := filepath.Join(lsm.ssTableDir, fmt.Sprintf("sstable_0_%d.sst", i))
		file, err := os.Open(sstablePath)
		if err == nil {
			scanner := bufio.NewScanner(file)
			for scanner.Scan() {
				line := scanner.Text()
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 && parts[0] == term {
					var diskRec Record
					json.Unmarshal([]byte(parts[1]), &diskRec)
					if diskRec.Tombstone {
						// Tombstone found on disk, overrides older data, break
						file.Close()
						return results
					}
					results = append(results, diskRec.MsgIDs...)
					break
				}
			}
			file.Close()
		}
	}

	// Also check Level 1 (compaction outputs)
	l1Files, _ := filepath.Glob(filepath.Join(lsm.ssTableDir, "sstable_1_*.sst"))
	for _, l1Path := range l1Files {
		file, err := os.Open(l1Path)
		if err == nil {
			scanner := bufio.NewScanner(file)
			for scanner.Scan() {
				line := scanner.Text()
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 && parts[0] == term {
					var diskRec Record
					json.Unmarshal([]byte(parts[1]), &diskRec)
					if diskRec.Tombstone {
						file.Close()
						return results
					}
					results = append(results, diskRec.MsgIDs...)
					break
				}
			}
			file.Close()
		}
	}

	// Final dedup
	set := make(map[string]bool)
	var dedup []string
	for _, r := range results {
		if !set[r] {
			set[r] = true
			dedup = append(dedup, r)
		}
	}
	return dedup
}
