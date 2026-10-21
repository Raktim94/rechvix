DROP TABLE IF EXISTS verifactu_submissions;
DROP TRIGGER IF EXISTS verifactu_records_no_truncate ON verifactu_records;
DROP TABLE IF EXISTS verifactu_records;
DROP FUNCTION IF EXISTS verifactu_records_chain_check();
DROP FUNCTION IF EXISTS verifactu_records_immutable();
DROP TABLE IF EXISTS verifactu_chain_heads;
DROP TABLE IF EXISTS verifactu_settings;
