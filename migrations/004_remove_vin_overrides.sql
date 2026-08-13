-- Neutralize personal test-VIN hardcoding.
-- Keep Avensis as a normal catalog model + generic Toyota/Avensis KB (not tied to any one VIN).

DELETE FROM known_vehicles;
DELETE FROM app_settings WHERE key IN ('primary_test_vin', 'primary_test_label');

-- Remove curated enrichment rows that were force-seeded for a specific car.
DELETE FROM vehicle_enrichment WHERE source = 'curated';

-- Optional: drop table if we are not using VIN overrides in-product yet.
DROP TABLE IF EXISTS known_vehicles;
