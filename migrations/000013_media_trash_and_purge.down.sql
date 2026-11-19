DROP INDEX IF EXISTS idx_stored_files_trash_purge;

ALTER TABLE stored_files
    DROP COLUMN IF EXISTS purged_at;
