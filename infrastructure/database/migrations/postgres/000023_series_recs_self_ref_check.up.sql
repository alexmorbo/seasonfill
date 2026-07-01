-- modify "series_recommendations" table
ALTER TABLE "series_recommendations" ADD CONSTRAINT "series_recommendations_no_self_ref" CHECK (recommended_series_id <> series_id);
