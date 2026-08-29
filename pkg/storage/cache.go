package storage

import (
	"sync"

	"poker-game-analyzer/pkg/table"
)

// MemoryCache provides a concurrent in-memory cache for live table states and player profiles.
type MemoryCache struct {
	mu          sync.RWMutex
	tableStates map[string]*table.HandState
	profiles    map[string]*LLMProfile
	playerStats map[string]*PlayerStats
}

// NewMemoryCache initializes an empty thread-safe MemoryCache.
func NewMemoryCache() *MemoryCache {
	return &MemoryCache{
		tableStates: make(map[string]*table.HandState),
		profiles:    make(map[string]*LLMProfile),
		playerStats: make(map[string]*PlayerStats),
	}
}

// SetTableState stores or updates the active HandState for a table.
func (c *MemoryCache) SetTableState(tableID string, state *table.HandState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if state == nil {
		delete(c.tableStates, tableID)
		return
	}
	c.tableStates[tableID] = state
}

// GetTableState retrieves the current HandState for a given tableID, or nil if not found.
func (c *MemoryCache) GetTableState(tableID string) *table.HandState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.tableStates[tableID]
}

// DeleteTableState removes the table state for tableID from the cache.
func (c *MemoryCache) DeleteTableState(tableID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.tableStates, tableID)
}

// SetProfile stores or updates the LLMProfile for a player.
func (c *MemoryCache) SetProfile(playerID string, prof *LLMProfile) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if prof == nil {
		delete(c.profiles, playerID)
		return
	}
	c.profiles[playerID] = prof
}

// GetProfile retrieves the LLMProfile for a given playerID, or nil if not found.
func (c *MemoryCache) GetProfile(playerID string) *LLMProfile {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.profiles[playerID]
}

// DeleteProfile removes the LLM profile for playerID from the cache.
func (c *MemoryCache) DeleteProfile(playerID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.profiles, playerID)
}

// SetPlayerStats stores or updates PlayerStats in memory.
func (c *MemoryCache) SetPlayerStats(playerID string, stats *PlayerStats) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if stats == nil {
		delete(c.playerStats, playerID)
		return
	}
	c.playerStats[playerID] = stats
}

// GetPlayerStats retrieves PlayerStats for playerID from cache, or nil if not found.
func (c *MemoryCache) GetPlayerStats(playerID string) *PlayerStats {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.playerStats[playerID]
}

// Clear clears all cached table states, profiles, and player stats.
func (c *MemoryCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tableStates = make(map[string]*table.HandState)
	c.profiles = make(map[string]*LLMProfile)
	c.playerStats = make(map[string]*PlayerStats)
}
