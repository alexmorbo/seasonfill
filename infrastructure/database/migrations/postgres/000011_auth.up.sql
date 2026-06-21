-- create "users" table
CREATE TABLE "users" (
  "id" bigserial NOT NULL,
  "username" text NOT NULL,
  "email" text NULL,
  "password_hash" text NULL,
  "oidc_subject" text NULL,
  "role" text NOT NULL DEFAULT 'admin',
  "avatar_mode" text NOT NULL DEFAULT 'auto',
  "preferred_language" text NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  "last_login_at" timestamptz NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "users_avatar_mode_check" CHECK (avatar_mode = ANY (ARRAY['auto'::text, 'monogram'::text, 'gravatar'::text])),
  CONSTRAINT "users_role_check" CHECK (role = ANY (ARRAY['admin'::text, 'user'::text]))
);
-- create index "users_oidc_subject_uniq" to table: "users"
CREATE UNIQUE INDEX "users_oidc_subject_uniq" ON "users" ("oidc_subject") WHERE (oidc_subject IS NOT NULL);
-- create index "users_username_uniq" to table: "users"
CREATE UNIQUE INDEX "users_username_uniq" ON "users" ("username");
-- create "user_instance_tags" table
CREATE TABLE "user_instance_tags" (
  "user_id" bigint NOT NULL,
  "instance_name" text NOT NULL,
  "sonarr_tag_id" integer NOT NULL,
  "sonarr_tag_label" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("user_id", "instance_name"),
  CONSTRAINT "user_instance_tags_instance_name_fkey" FOREIGN KEY ("instance_name") REFERENCES "sonarr_instance" ("name") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "user_instance_tags_user_id_fkey" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- create index "user_instance_tags_label" to table: "user_instance_tags"
CREATE UNIQUE INDEX "user_instance_tags_label" ON "user_instance_tags" ("instance_name", "sonarr_tag_label");
