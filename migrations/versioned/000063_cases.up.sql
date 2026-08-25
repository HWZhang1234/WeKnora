-- Migration: 000063_cases
-- Stores structured Case attributes while reusing normal Knowledge/Chunk indexing.
DO $$ BEGIN RAISE NOTICE '[Migration 000063] Creating Case tables'; END $$;

CREATE TABLE IF NOT EXISTS cases (
    id                  VARCHAR(36) PRIMARY KEY,
    tenant_id           BIGINT NOT NULL,
    knowledge_base_id   VARCHAR(36) NOT NULL,
    knowledge_id        VARCHAR(36) NOT NULL,
    case_number         VARCHAR(64) NOT NULL,
    subject             VARCHAR(1000),
    description         TEXT,
    summary             TEXT,
    problem_area_1      VARCHAR(255),
    problem_area_2      VARCHAR(255),
    problem_area_3      VARCHAR(255),
    case_url            TEXT,
    case_date           DATE,
    content_hash        VARCHAR(64) NOT NULL,
    extra_attributes    JSONB,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_cases_tenant_case UNIQUE (tenant_id, case_number),
    CONSTRAINT uq_cases_knowledge UNIQUE (knowledge_id),
    CONSTRAINT fk_cases_knowledge FOREIGN KEY (knowledge_id) REFERENCES knowledges(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_cases_tenant_kb ON cases (tenant_id, knowledge_base_id);
CREATE INDEX IF NOT EXISTS idx_cases_problem_area_1 ON cases (problem_area_1);
CREATE INDEX IF NOT EXISTS idx_cases_problem_area_2 ON cases (problem_area_2);
CREATE INDEX IF NOT EXISTS idx_cases_problem_area_3 ON cases (problem_area_3);
CREATE INDEX IF NOT EXISTS idx_cases_case_date ON cases (case_date);

CREATE TABLE IF NOT EXISTS case_chipsets (
    case_id VARCHAR(36) NOT NULL REFERENCES cases(id) ON DELETE CASCADE,
    chipset VARCHAR(128) NOT NULL,
    PRIMARY KEY (case_id, chipset)
);
CREATE INDEX IF NOT EXISTS idx_case_chipsets_chipset ON case_chipsets (chipset);

CREATE TABLE IF NOT EXISTS case_change_requests (
    case_id VARCHAR(36) NOT NULL REFERENCES cases(id) ON DELETE CASCADE,
    change_request_number VARCHAR(64) NOT NULL,
    PRIMARY KEY (case_id, change_request_number)
);
CREATE INDEX IF NOT EXISTS idx_case_change_requests_number ON case_change_requests (change_request_number);

DO $$ BEGIN RAISE NOTICE '[Migration 000063] Case tables created'; END $$;
