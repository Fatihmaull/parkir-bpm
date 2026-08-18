-- Seed dev/testing multi-lahan & multi-gerbang (task 5.4) — BUKAN untuk produksi.
--
-- Idempoten (ON CONFLICT DO NOTHING) — aman dijalankan berulang kali terhadap DB dev yang
-- sama. UUID di-hardcode (bukan gen_random_uuid()) supaya id-nya bisa dirujuk di dokumentasi
-- lokal, skrip lain, atau .env dev tanpa perlu query balik dulu.
--
-- Cakupan sengaja "multi": 2 tenant, tenant pertama 2 site (uji isolasi ANTAR site dalam SATU
-- tenant, kasus paling gampang bocor), tenant kedua 1 site (uji isolasi ANTAR tenant). Site
-- pertama dapat 2 gerbang masuk (uji `gate_code` benar-benar membedakan, bukan asumsi 1 gerbang
-- masuk per lahan — lihat K-catatan gatesvc).
--
-- Pakai:  psql "$EDGE_DATABASE_URL" -f db/seed/dev_seed.sql   (atau `make seed`)

-- ── Tenant 1: dua lahan ──
INSERT INTO tenants (id, code, name) VALUES
    ('00000000-0000-0000-0000-000000000001', 'dev_jabar', 'Jabar Creative (dev)')
ON CONFLICT (id) DO NOTHING;

INSERT INTO sites (id, tenant_id, code, name, city) VALUES
    ('00000000-0000-0000-0000-000000000011', '00000000-0000-0000-0000-000000000001',
     'mall_jabar', 'Mall Jabar (dev)', 'Bandung'),
    ('00000000-0000-0000-0000-000000000012', '00000000-0000-0000-0000-000000000001',
     'plaza_cimahi', 'Plaza Cimahi (dev)', 'Cimahi')
ON CONFLICT (id) DO NOTHING;

-- ── Tenant 2: satu lahan (uji isolasi ANTAR tenant) ──
INSERT INTO tenants (id, code, name) VALUES
    ('00000000-0000-0000-0000-000000000002', 'dev_lain', 'Tenant Lain (dev)')
ON CONFLICT (id) DO NOTHING;

INSERT INTO sites (id, tenant_id, code, name, city) VALUES
    ('00000000-0000-0000-0000-000000000021', '00000000-0000-0000-0000-000000000002',
     'plaza_lain', 'Plaza Tenant Lain (dev)', 'Jakarta')
ON CONFLICT (id) DO NOTHING;

-- ── Gerbang: mall_jabar dapat 2 gerbang masuk + 1 keluar (multi-gerbang, PRD v3) ──
INSERT INTO gates (id, site_id, code, kind, controller_addr, transport, endpoint) VALUES
    ('00000000-0000-0000-0000-000000000111', '00000000-0000-0000-0000-000000000011',
     'GATE-IN-01', 'ENTRY', 1, 'sim', '127.0.0.1:56001'),
    ('00000000-0000-0000-0000-000000000112', '00000000-0000-0000-0000-000000000011',
     'GATE-IN-02', 'ENTRY', 2, 'sim', '127.0.0.1:56002'),
    ('00000000-0000-0000-0000-000000000113', '00000000-0000-0000-0000-000000000011',
     'GATE-OUT-01', 'EXIT', 3, 'sim', '127.0.0.1:56003'),
    ('00000000-0000-0000-0000-000000000121', '00000000-0000-0000-0000-000000000012',
     'GATE-IN-01', 'ENTRY', 1, 'sim', '127.0.0.1:56011'),
    ('00000000-0000-0000-0000-000000000122', '00000000-0000-0000-0000-000000000012',
     'GATE-OUT-01', 'EXIT', 2, 'sim', '127.0.0.1:56012'),
    ('00000000-0000-0000-0000-000000000211', '00000000-0000-0000-0000-000000000021',
     'GATE-IN-01', 'ENTRY', 1, 'sim', '127.0.0.1:56021'),
    ('00000000-0000-0000-0000-000000000212', '00000000-0000-0000-0000-000000000021',
     'GATE-OUT-01', 'EXIT', 2, 'sim', '127.0.0.1:56022')
ON CONFLICT (id) DO NOTHING;

-- ── Tarif (D5: versioned, baris ini adalah versi pertama tiap site/jenis) ──
INSERT INTO tariffs (id, site_id, vehicle_type, base_rate, first_hour_rate) VALUES
    ('00000000-0000-0000-0000-000000000311', '00000000-0000-0000-0000-000000000011', 'mobil', 5000, 5000),
    ('00000000-0000-0000-0000-000000000312', '00000000-0000-0000-0000-000000000011', 'motor', 2000, 2000),
    ('00000000-0000-0000-0000-000000000321', '00000000-0000-0000-0000-000000000012', 'mobil', 4000, 4000),
    ('00000000-0000-0000-0000-000000000322', '00000000-0000-0000-0000-000000000012', 'motor', 2000, 2000),
    ('00000000-0000-0000-0000-000000000331', '00000000-0000-0000-0000-000000000021', 'mobil', 6000, 6000)
ON CONFLICT (id) DO NOTHING;

-- ── Member contoh (anti-passback §8.2, dua tenant supaya kelihatan tak saling terlihat) ──
INSERT INTO memberships (id, tenant_id, rfid_uid, holder_name, plates, vehicle_type, valid_from, valid_until) VALUES
    ('00000000-0000-0000-0000-000000000411', '00000000-0000-0000-0000-000000000001',
     '04A1B2C3', 'Member Dev Jabar', ARRAY['D1234ABC'], 'mobil', CURRENT_DATE, CURRENT_DATE + INTERVAL '1 year'),
    ('00000000-0000-0000-0000-000000000421', '00000000-0000-0000-0000-000000000002',
     '04D4E5F6', 'Member Dev Lain', ARRAY['B5678XYZ'], 'mobil', CURRENT_DATE, CURRENT_DATE + INTERVAL '1 year')
ON CONFLICT (id) DO NOTHING;
