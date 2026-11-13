ALTER TABLE public_event_ingestions
    ADD COLUMN failure_reason text NOT NULL DEFAULT '';
