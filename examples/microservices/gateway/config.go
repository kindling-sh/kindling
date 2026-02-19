package main

// cfg holds runtime configuration, loaded once from environment
// variables at startup. Nothing fancy — just a struct.
type cfg struct {
	ListenAddr   string
	OrdersURL    string
	InventoryURL string
}

func loadConfig() cfg {
	return cfg{
		ListenAddr:   envOr("LISTEN_ADDR", ":9090"),
		OrdersURL:    mustEnv("ORDERS_URL"),
		InventoryURL: mustEnv("INVENTORY_URL"),
	}
}
