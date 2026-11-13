CREATE OR REPLACE FUNCTION prevent_posted_move_mutation() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        IF OLD.status IN ('posted', 'reversed') THEN
            RAISE EXCEPTION 'Cannot delete posted move (id=%).', OLD.id
                USING ERRCODE = 'restrict_violation';
        END IF;
        RETURN OLD;
    END IF;

    IF OLD.status = 'posted' THEN
        IF NEW.status = 'reversed'
           AND NEW.reversed_move_id IS NOT NULL
           AND OLD.reversed_move_id IS NULL
           AND (to_jsonb(NEW) - ARRAY['status', 'reversed_move_id', 'updated_at', 'entry'])
               IS NOT DISTINCT FROM
               (to_jsonb(OLD) - ARRAY['status', 'reversed_move_id', 'updated_at', 'entry'])
           AND (NEW.entry - ARRAY['Status', 'ReversedEntryID', 'UpdatedAt'])
               IS NOT DISTINCT FROM
               (OLD.entry - ARRAY['Status', 'ReversedEntryID', 'UpdatedAt'])
           AND NEW.entry->>'Status' = 'reversed'
           AND NEW.entry->>'ReversedEntryID' IS NOT NULL
           AND NEW.entry->>'ReversedEntryID' = NEW.reversed_move_id::text
           AND EXISTS (
               SELECT 1 FROM account_moves reversal
               WHERE reversal.id = NEW.reversed_move_id
                 AND reversal.company_id = OLD.company_id
                 AND reversal.status = 'posted'
                 AND reversal.entry->>'Status' = 'posted'
                 AND reversal.entry->>'OriginalEntryID' = OLD.id::text
           ) THEN
            RETURN NEW;
        END IF;
        RAISE EXCEPTION
            'Cannot modify posted move (id=%). Use reversal entry instead.',
            OLD.id USING ERRCODE = 'restrict_violation';
    END IF;

    IF OLD.status = 'reversed' THEN
        RAISE EXCEPTION 'Cannot modify reversed move (id=%).', OLD.id
            USING ERRCODE = 'restrict_violation';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_prevent_posted_move_mutation ON account_moves;
CREATE TRIGGER trg_prevent_posted_move_mutation
    BEFORE UPDATE OR DELETE ON account_moves
    FOR EACH ROW EXECUTE FUNCTION prevent_posted_move_mutation();

CREATE OR REPLACE FUNCTION prevent_direct_posted_move_insert() RETURNS trigger AS $$
BEGIN
    IF NEW.status <> 'draft' THEN
        RAISE EXCEPTION 'Moves must be inserted as drafts (id=%).', NEW.id
            USING ERRCODE = 'restrict_violation';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_prevent_direct_posted_move_insert ON account_moves;
CREATE TRIGGER trg_prevent_direct_posted_move_insert
    BEFORE INSERT ON account_moves
    FOR EACH ROW EXECUTE FUNCTION prevent_direct_posted_move_insert();

CREATE OR REPLACE FUNCTION protect_posted_move_lines() RETURNS trigger AS $$
DECLARE
    old_move_status varchar(16);
    new_move_status varchar(16);
BEGIN
    IF TG_OP IN ('UPDATE', 'DELETE') THEN
        SELECT status INTO old_move_status
        FROM account_moves WHERE id = OLD.move_id;
        IF old_move_status IS DISTINCT FROM 'draft' THEN
            RAISE EXCEPTION
                'Cannot modify line belonging to non-draft move (move_id=%)',
                OLD.move_id USING ERRCODE = 'restrict_violation';
        END IF;
    END IF;

    IF TG_OP IN ('INSERT', 'UPDATE') THEN
        IF TG_OP = 'INSERT' OR OLD.move_id IS DISTINCT FROM NEW.move_id THEN
            SELECT status INTO new_move_status
            FROM account_moves WHERE id = NEW.move_id;
            IF new_move_status IS DISTINCT FROM 'draft' THEN
                RAISE EXCEPTION
                    'Cannot attach line to non-draft move (move_id=%)',
                    NEW.move_id USING ERRCODE = 'restrict_violation';
            END IF;
        END IF;
    END IF;

    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_immutable_posted_lines ON account_move_lines;
DROP TRIGGER IF EXISTS trg_protect_posted_move_lines ON account_move_lines;
CREATE TRIGGER trg_protect_posted_move_lines
    BEFORE INSERT OR UPDATE OR DELETE ON account_move_lines
    FOR EACH ROW EXECUTE FUNCTION protect_posted_move_lines();

CREATE OR REPLACE FUNCTION check_posted_move_balance_statement() RETURNS trigger AS $$
DECLARE
    unbalanced_move record;
BEGIN
    SELECT posted_moves.id,
           posted_moves.entry->>'Status' AS entry_status,
           COALESCE(SUM(lines.debit), 0) AS total_debit,
           COALESCE(SUM(lines.credit), 0) AS total_credit
    INTO unbalanced_move
    FROM new_rows AS posted_moves
    JOIN old_rows AS prior_moves ON prior_moves.id = posted_moves.id
    LEFT JOIN account_move_lines AS lines ON lines.move_id = posted_moves.id
    WHERE prior_moves.status = 'draft'
      AND posted_moves.status = 'posted'
    GROUP BY posted_moves.id, posted_moves.entry->>'Status'
    HAVING posted_moves.entry->>'Status' IS DISTINCT FROM 'posted'
        OR COALESCE(SUM(lines.debit), 0) <> COALESCE(SUM(lines.credit), 0)
    LIMIT 1;

    IF FOUND THEN
        IF unbalanced_move.entry_status IS DISTINCT FROM 'posted' THEN
            RAISE EXCEPTION
                'Move % has inconsistent status in entry JSON: %',
                unbalanced_move.id, unbalanced_move.entry_status
                USING ERRCODE = 'check_violation';
        END IF;
        RAISE EXCEPTION
            'Move % is unbalanced: total_debit=% != total_credit=%',
            unbalanced_move.id, unbalanced_move.total_debit, unbalanced_move.total_credit
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_check_posted_move_balance_header ON account_moves;
DROP TRIGGER IF EXISTS trg_check_posted_move_balance_statement ON account_moves;
CREATE TRIGGER trg_check_posted_move_balance_statement
    AFTER UPDATE ON account_moves
    REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows
    FOR EACH STATEMENT
    EXECUTE FUNCTION check_posted_move_balance_statement();
