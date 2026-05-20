package action

import (
	"errors"
	"sort"
	"sync"
)

// Catalog stores domain action contracts by name.
type Catalog struct {
	mu        sync.RWMutex
	contracts map[string]ActionContract
	order     []string
}

// NewCatalog creates an empty action catalog.
func NewCatalog() *Catalog {
	return &Catalog{contracts: map[string]ActionContract{}}
}

// Register adds a contract to the catalog.
func (c *Catalog) Register(contract ActionContract) error {
	if contract.Name == "" {
		return errors.Join(ErrInvalidContract, errors.New("action contract name is required"))
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.contracts[contract.Name]; ok {
		return errors.Join(ErrDuplicateAction, errors.New(contract.Name))
	}
	c.contracts[contract.Name] = contract
	c.order = append(c.order, contract.Name)
	sort.Strings(c.order)
	return nil
}

// Get returns a contract by name.
func (c *Catalog) Get(name string) (ActionContract, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	contract, ok := c.contracts[name]
	return contract, ok
}

// List returns all contracts in stable name order.
func (c *Catalog) List() []ActionContract {
	c.mu.RLock()
	defer c.mu.RUnlock()

	contracts := make([]ActionContract, 0, len(c.order))
	for _, name := range c.order {
		contracts = append(contracts, c.contracts[name])
	}
	return contracts
}
