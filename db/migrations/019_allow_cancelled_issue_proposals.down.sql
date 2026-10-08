DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM work_item
        WHERE status = 'cancelled' AND external_number IS NULL AND external_url = ''
    ) THEN
        RAISE EXCEPTION 'cannot restore the previous work_item constraint while cancelled proposals without external issues exist';
    END IF;
END;
$$;

ALTER TABLE work_item DROP CONSTRAINT work_item_external_reference_check;

ALTER TABLE work_item
    ADD CONSTRAINT work_item_check1 CHECK (
        (status = 'proposed' AND external_number IS NULL AND external_url = '') OR
        (status <> 'proposed' AND external_number IS NOT NULL AND external_number > 0 AND external_url <> '')
    );
