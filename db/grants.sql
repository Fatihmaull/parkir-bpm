-- Dijalankan SEKALI setelah migrasi & setelah role aplikasi `app_user` dibuat.
-- Menegakkan append-only pada audit_logs di level privilege (PRD §9.2), melengkapi
-- trigger di migrasi 00005. Bukan bagian dari goose agar tidak gagal saat role belum ada.
--
--   CREATE ROLE app_user LOGIN PASSWORD '...';   -- kredensial dari .env, bukan di sini
--   GRANT SELECT, INSERT ON audit_logs TO app_user;

REVOKE UPDATE, DELETE, TRUNCATE ON audit_logs FROM app_user;
