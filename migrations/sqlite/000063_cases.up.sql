CREATE TABLE IF NOT EXISTS cases (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL REFERENCES knowledges(id) ON DELETE CASCADE,
    case_number VARCHAR(64) NOT NULL,
    subject VARCHAR(1000),
    description TEXT,
    summary TEXT,
    problem_area_1 VARCHAR(255),
    problem_area_2 VARCHAR(255),
    problem_area_3 VARCHAR(255),
    case_url TEXT,
    case_date DATE,
    content_hash VARCHAR(64) NOT NULL,
    extra_attributes TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (tenant_id, case_number),
    UNIQUE (knowledge_id)
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
