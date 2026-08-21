-- create "torrent_movie_map" table
CREATE TABLE "torrent_movie_map" (
  "instance_name" text NOT NULL,
  "torrent_hash" text NOT NULL,
  "radarr_movie_id" bigint NOT NULL,
  "source" text NOT NULL,
  "provenance" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("instance_name", "torrent_hash"),
  CONSTRAINT "torrent_movie_map_instance_name_fkey" FOREIGN KEY ("instance_name") REFERENCES "arr_instance" ("name") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- create index "torrent_movie_map_movie_idx" to table: "torrent_movie_map"
CREATE INDEX "torrent_movie_map_movie_idx" ON "torrent_movie_map" ("instance_name", "radarr_movie_id");
