ALTER TABLE transfers ADD COLUMN IF NOT EXISTS transfer_mode INT NOT NULL DEFAULT 1;
-- 1 = RELAY (store-and-forward), 2 = P2P_DIRECT, 3 = P2P_RELAYED
