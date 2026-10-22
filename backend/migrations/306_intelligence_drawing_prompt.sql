-- 306_intelligence_drawing_prompt.sql stores administrator drawing instructions
-- alongside each model configuration so local generation can use custom tasks.
-- Empty instructions select the service's default pelican animation, and the
-- additive column keeps idle configurations readable by earlier binaries.
ALTER TABLE intelligence_test_configs
    ADD COLUMN IF NOT EXISTS prompt TEXT NOT NULL DEFAULT '';
