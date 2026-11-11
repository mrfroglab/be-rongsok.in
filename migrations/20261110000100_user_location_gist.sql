-- Index spasial untuk pencarian pengepul terdekat (ST_DWithin / ST_Distance).
-- CreateIndex
CREATE INDEX "User_location_idx" ON "User" USING GIST ("location");
