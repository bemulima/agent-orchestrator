ALTER TABLE work_item DROP CONSTRAINT work_item_check1;

ALTER TABLE work_item
    ADD CONSTRAINT work_item_external_reference_check CHECK (
        (status = 'proposed' AND external_number IS NULL AND external_url = '') OR
        (status IN ('published', 'closed') AND external_number IS NOT NULL AND external_number > 0 AND external_url <> '') OR
        (status = 'cancelled' AND (
            (external_number IS NULL AND external_url = '') OR
            (external_number IS NOT NULL AND external_number > 0 AND external_url <> '')
        ))
    );
