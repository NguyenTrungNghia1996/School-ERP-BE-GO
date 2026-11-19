-- Thêm cột purged_at để hỗ trợ Thùng rác (Soft-Delete) và Job dọn file trên R2 sau N ngày
ALTER TABLE stored_files
    ADD COLUMN IF NOT EXISTS purged_at TIMESTAMPTZ;

-- Các bản ghi đã xóa mềm trước migration này vốn đã bị xóa vật lý trên R2 ngay lập tức
UPDATE stored_files
SET purged_at = deleted_at
WHERE deleted_at IS NOT NULL
  AND purged_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_stored_files_trash_purge
    ON stored_files (deleted_at)
    WHERE deleted_at IS NOT NULL AND purged_at IS NULL;
