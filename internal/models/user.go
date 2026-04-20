package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type User struct {
	Id                    string     `json:"id"`
	Email                 string     `json:"email"`
	StripeCustomer_Id     *string    `json:"stripe_customer_id"`
	StripePaymentMethodId *string    `json:"stripe_payment_method_id"`
	LastBilledAt          *time.Time `json:"last_billed_at"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
}

func MigrateUserTable(pool *pgxpool.Pool) error {
	ctx := context.Background()
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS users (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			email TEXT NOT NULL,
			stripe_customer_id TEXT,
			stripe_payment_method_id TEXT,
			last_billed_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
	`)
	if err != nil {
		return err
	}

	var duplicateEmail string
	var duplicateCount int
	err = pool.QueryRow(ctx, `
		SELECT email, COUNT(*)
		FROM users
		GROUP BY email
		HAVING COUNT(*) > 1
		LIMIT 1
	`).Scan(&duplicateEmail, &duplicateCount)
	if err == nil {
		return fmt.Errorf("users table has duplicate email values; resolve duplicates before enabling uniqueness (email=%s count=%d)", duplicateEmail, duplicateCount)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("failed to validate users email uniqueness: %w", err)
	}

	var duplicateStripeCustomerID string
	err = pool.QueryRow(ctx, `
		SELECT stripe_customer_id, COUNT(*)
		FROM users
		WHERE stripe_customer_id IS NOT NULL
		GROUP BY stripe_customer_id
		HAVING COUNT(*) > 1
		LIMIT 1
	`).Scan(&duplicateStripeCustomerID, &duplicateCount)
	if err == nil {
		return fmt.Errorf("users table has duplicate stripe_customer_id values; resolve duplicates before enabling uniqueness (stripe_customer_id=%s count=%d)", duplicateStripeCustomerID, duplicateCount)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("failed to validate users stripe_customer_id uniqueness: %w", err)
	}

	_, err = pool.Exec(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS users_email_unique_idx ON users (email)`)
	if err != nil {
		return fmt.Errorf("failed to create users email unique index: %w", err)
	}

	_, err = pool.Exec(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS users_stripe_customer_id_unique_idx ON users (stripe_customer_id) WHERE stripe_customer_id IS NOT NULL`)
	if err != nil {
		return fmt.Errorf("failed to create users stripe customer unique index: %w", err)
	}

	err = migrateSupabaseTokenHook(ctx, pool)
	if err != nil {
		return fmt.Errorf("failed to migrate Supabase token hook: %w", err)
	}

	return nil
}

func migrateSupabaseTokenHook(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
		CREATE OR REPLACE FUNCTION public.custom_access_token_hook(event jsonb)
		RETURNS jsonb
		LANGUAGE plpgsql
		STABLE
		AS $$
		DECLARE
			claims jsonb;
			user_metadata jsonb;
			auth_email text;
			app_user jsonb;
		BEGIN
			claims := COALESCE(event->'claims', '{}'::jsonb);

			auth_email := lower(trim(COALESCE(claims->>'email', '')));

			IF auth_email <> '' THEN
				SELECT to_jsonb(u)
				INTO app_user
				FROM public.users u
				WHERE lower(trim(u.email)) = auth_email
				LIMIT 1;
			END IF;

			user_metadata := COALESCE(claims->'user_metadata', '{}'::jsonb);

			IF app_user IS NOT NULL THEN
				user_metadata := jsonb_set(user_metadata, '{app_user}', app_user, true);
			ELSE
				user_metadata := jsonb_set(user_metadata, '{app_user}', 'null'::jsonb, true);
			END IF;

			claims := jsonb_set(claims, '{user_metadata}', user_metadata, true);
			event := jsonb_set(event, '{claims}', claims, true);

			RETURN event;
		END;
		$$;
	`)
	if err != nil {
		return err
	}

	_, err = pool.Exec(ctx, `
		DO $$
		BEGIN
			IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'supabase_auth_admin') THEN
				GRANT USAGE ON SCHEMA public TO supabase_auth_admin;
				GRANT EXECUTE ON FUNCTION public.custom_access_token_hook TO supabase_auth_admin;
				GRANT SELECT ON TABLE public.users TO supabase_auth_admin;
			END IF;
		END;
		$$;
	`)
	if err != nil {
		return err
	}

	_, err = pool.Exec(ctx, `
		DO $$
		BEGIN
			IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'authenticated') THEN
				REVOKE EXECUTE ON FUNCTION public.custom_access_token_hook FROM authenticated;
				REVOKE ALL ON TABLE public.users FROM authenticated;
			END IF;

			IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'anon') THEN
				REVOKE EXECUTE ON FUNCTION public.custom_access_token_hook FROM anon;
				REVOKE ALL ON TABLE public.users FROM anon;
			END IF;

			REVOKE EXECUTE ON FUNCTION public.custom_access_token_hook FROM PUBLIC;
			REVOKE ALL ON TABLE public.users FROM PUBLIC;
		END;
		$$;
	`)
	if err != nil {
		return err
	}

	_, err = pool.Exec(ctx, `
		DO $$
		BEGIN
			IF EXISTS (
				SELECT 1
				FROM pg_roles
				WHERE rolname = 'supabase_auth_admin'
			) AND EXISTS (
				SELECT 1
				FROM pg_class c
				JOIN pg_namespace n ON n.oid = c.relnamespace
				WHERE n.nspname = 'public'
				  AND c.relname = 'users'
				  AND c.relkind = 'r'
				  AND c.relrowsecurity = true
			) AND NOT EXISTS (
				SELECT 1
				FROM pg_policies
				WHERE schemaname = 'public'
				  AND tablename = 'users'
				  AND policyname = 'Allow auth admin to read users'
			) THEN
				CREATE POLICY "Allow auth admin to read users"
				ON public.users
				AS PERMISSIVE
				FOR SELECT
				TO supabase_auth_admin
				USING (true);
			END IF;
		END;
		$$;
	`)

	return err
}
