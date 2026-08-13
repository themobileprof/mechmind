-- Toyota Avensis as a normal Nigeria-market catalog model (2010+), plus model-scoped KB.
-- No VIN-specific overrides — identity must come from OBD Mode 09 / technician entry / vPIC.

INSERT INTO market_models (make_id, name, nhtsa_name, year_from, year_to)
SELECT m.id, 'Avensis', 'AVENSIS', 2010, 2018
FROM market_makes m
WHERE m.name = 'Toyota'
ON CONFLICT (make_id, name) DO UPDATE SET
    year_from = LEAST(market_models.year_from, EXCLUDED.year_from),
    year_to = GREATEST(COALESCE(market_models.year_to, EXCLUDED.year_to), EXCLUDED.year_to);

INSERT INTO knowledge_articles (code, vehicle_scope, title, summary, likely_causes, tests, parts) VALUES
(
    'P0171', 'TOYOTA',
    'Toyota System Too Lean (Bank 1) — 2010+ triage',
    'On 2010+ Toyota petrol (including Europe-spec models), lean codes are often intake boots, PCV side air, or MAF contamination — not first-line O2 replacement.',
    ARRAY['Cracked intake boot between MAF and throttle','PCV hose / valve leak','Dirty MAF','Low fuel pressure / weak pump','Exhaust leak before upstream O2'],
    ARRAY['Graph STFT/LTFT idle vs 2500 rpm','Smoke-test intake + PCV','MAF g/s vs expected','Fuel pressure under load','Inspect upstream O2 for exhaust tick'],
    ARRAY['Intake boot','PCV valve/hose','MAF sensor','Fuel filter']
),
(
    'P0011', 'TOYOTA',
    'Toyota Camshaft Position Timing Over-Advanced (Bank 1)',
    'Common on Valvematic / VVT-i ZR engines when oil is wrong grade, sludge restricts the oil control valve, or the VVT actuator sticks.',
    ARRAY['Incorrect/old engine oil','Clogged VVT oil control valve (OCV)','VVT actuator failure','Cam timing chain stretch (higher miles)','Low oil pressure'],
    ARRAY['Verify oil grade and level','Remove/clean OCV screen','Compare cam/crank correlation PID','Listen for cold-start rattle'],
    ARRAY['Engine oil + filter','VVT oil control valve','VVT actuator','Timing chain kit (if stretch confirmed)']
),
(
    'P0016', 'TOYOTA',
    'Toyota Crank–Cam Correlation (Bank 1 Sensor A)',
    'Correlation faults after timing work, jumped chain, or failed cam/crank sensors. Confirm mechanical timing before replacing ECM.',
    ARRAY['Stretched/jumped timing chain','Failed cam or crank sensor','Incorrectly installed timing components','Wiring/connector fault to sensors'],
    ARRAY['Scope cam vs crank pattern','Inspect chain guides/tensioner','Check sensor connectors for oil intrusion','Verify mechanical timing marks'],
    ARRAY['Camshaft position sensor','Crankshaft position sensor','Timing chain kit']
),
(
    'P0420', 'TOYOTA',
    'Toyota Catalyst Efficiency Bank 1',
    'Confirm upstream misfire/lean history and exhaust leaks before condemning the cat. Downstream O2 aging is common on higher-mileage cars.',
    ARRAY['Aged catalytic converter','Downstream O2 slow/lazy','Exhaust leak before rear O2','Prior misfire / rich-lean damage'],
    ARRAY['Compare upstream vs downstream O2 activity','Review misfire + fuel trim history','Check for exhaust leaks at flex/joints','Temperature delta across cat if accessible'],
    ARRAY['Downstream O2 sensor','Catalytic converter','Exhaust gasket/flex']
),
(
    'P0300', 'AVENSIS',
    'Avensis Random Misfire (2010+)',
    'Coil-on-plug misfires are frequent; also check VVT/Valvematic-related lean conditions before parts-cannon coils alone.',
    ARRAY['Ignition coils','Spark plugs worn/incorrect gap','Lean condition (see P0171)','Injector imbalance','Low compression on one cylinder'],
    ARRAY['Per-cylinder misfire counters','Swap coils between cylinders','Fuel trims and Mode$06','Relative compression / power balance'],
    ARRAY['Ignition coils','Spark plugs','Injectors']
),
(
    'U0100', 'AVENSIS',
    'Avensis Lost Communication With ECM',
    'Check EFI relay, under-hood fuse box corrosion, and ground points near battery before replacing ECM.',
    ARRAY['EFI/main relay','Blown EFI fuse','ECM ground corrosion','CAN wiring damage','Weak battery during crank'],
    ARRAY['Battery/charging test','Power/ground at ECM with load','Relay swap test','Network module list — which nodes alive'],
    ARRAY['EFI relay','Fuses','Battery','ECM grounds/wiring']
),
(
    'C0035', 'AVENSIS',
    'Avensis Left Front Wheel Speed Sensor',
    'ABS/ESP lamp with C0035/C0040 is common on rough roads — hub corrosion, damaged harness at strut, packed debris.',
    ARRAY['LF wheel speed sensor failed','Tone ring damaged','Harness chafe at strut','ABS module input (less common)'],
    ARRAY['Compare all four wheel speeds at walking pace','Inspect connector for green corrosion','Check tone ring teeth','Ohm/sensor air gap per OEM'],
    ARRAY['Wheel speed sensor','Hub/tone ring','ABS harness repair']
)
ON CONFLICT (code, vehicle_scope) DO UPDATE SET
    title = EXCLUDED.title,
    summary = EXCLUDED.summary,
    likely_causes = EXCLUDED.likely_causes,
    tests = EXCLUDED.tests,
    parts = EXCLUDED.parts,
    updated_at = now();
