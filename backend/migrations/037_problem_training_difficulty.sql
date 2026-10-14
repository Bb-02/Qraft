-- Authoring targets are independent of measured or administrator-confirmed
-- ratings. Introductory sets may use targets below 800 or between CF bands.
ALTER TABLE problems DROP CONSTRAINT problems_difficulty_check;
ALTER TABLE problems ADD CONSTRAINT problems_difficulty_check
    CHECK (difficulty BETWEEN 1 AND 10000);

COMMENT ON COLUMN problems.difficulty IS
    'Author-requested integer training target (1-10000); not an official rating';
