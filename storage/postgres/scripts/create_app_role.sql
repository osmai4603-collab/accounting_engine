-- Provision the restricted application role used by the accounting engine.
--
-- The role is deliberately a non-owner and is denied the ability to bypass
-- row-level security. All tenant isolation therefore depends on the
-- app.current_company_id session setting, exactly as in production.
--
-- Usage:
--   psql "$DATABASE_URL" -v app_password='<strong-password>' \
--        -f storage/postgres/scripts/create_app_role.sql
--
-- The script is idempotent: re-running it keeps the role, resets NOBYPASSRLS,
-- and rotates the password.

\set ON_ERROR_STOP on

SELECT format('CREATE ROLE accounting_app_user LOGIN NOBYPASSRLS PASSWORD %L', :'app_password')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'accounting_app_user')
\gexec

ALTER ROLE accounting_app_user LOGIN NOBYPASSRLS;
ALTER ROLE accounting_app_user PASSWORD :'app_password';

-- The role must never own schema objects and must never be a superuser.
ALTER ROLE accounting_app_user NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION;

-- Grant connection and schema usage. Table-level DML grants are intentionally
-- left to the deployment's migration/grant policy so that the application only
-- receives the privileges it actually needs.
GRANT CONNECT ON DATABASE :"DBNAME" TO accounting_app_user;
GRANT USAGE ON SCHEMA public TO accounting_app_user;

-- Verification: the role must be a non-superuser without BYPASSRLS.
DO $$
DECLARE
    role_is_super boolean;
    role_bypasses_rls boolean;
BEGIN
    SELECT rolsuper, rolbypassrls INTO role_is_super, role_bypasses_rls
    FROM pg_roles WHERE rolname = 'accounting_app_user';
    IF role_is_super OR role_bypasses_rls THEN
        RAISE EXCEPTION 'accounting_app_user must be NOSUPERUSER and NOBYPASSRLS (super=%, bypassrls=%)',
            role_is_super, role_bypasses_rls;
    END IF;
END $$;
