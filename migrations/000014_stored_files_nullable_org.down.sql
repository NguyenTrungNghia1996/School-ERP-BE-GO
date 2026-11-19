DROP INDEX IF EXISTS idx_stored_files_platform_created;

DELETE FROM stored_files WHERE organization_id IS NULL;

ALTER TABLE stored_files
    ALTER COLUMN organization_id SET NOT NULL;
