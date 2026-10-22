import type { IntelligenceConfig, IntelligenceRun, IntelligenceTest } from '@/api/intelligence'

// fixtures.ts builds synthetic intelligence configurations, tests, and run observations for
// component suites. Each builder accepts field overrides so individual scenarios can model
// status, detail availability, and generated artifacts without storing live provider
// credentials.

// Fixture observations reproduce result fields with synthetic identities, keeping component tests independent of live credentials and remote model endpoints.
export const runFixture = (overrides: Partial<IntelligenceRun> = {}): IntelligenceRun => ({
  id: 'run-1',
  config_id: 1,
  group_id: 1,
  model: 'model-a',
  benchmark: 'candy',
  status: 'completed',
  verdict: 'passed',
  has_detail: true,
  has_artifact: false,
  created_at: '2026-10-03T01:00:00Z',
  updated_at: '2026-10-03T01:01:00Z',
  ...overrides,
})
export const testFixture = (overrides: Partial<IntelligenceTest> = {}): IntelligenceTest => ({
  id: 1,
  group_id: 1,
  group_name: 'Group A',
  model: 'model-a',
  benchmark: 'candy',
  runs: [],
  artifacts: [],
  ...overrides,
})
// configFixture supplies an editable configuration with optional drawing instructions.
export const configFixture = (overrides: Partial<IntelligenceConfig> = {}): IntelligenceConfig => ({
  id: 1,
  group_id: 1,
  group_name: 'Group A',
  model: 'model-a',
  benchmark: 'candy',
  prompt: '',
  base_url: 'https://gateway.example/v1',
  api_key_configured: true,
  protocol: 'responses',
  reasoning_effort: '',
  service_tier: '',
  enabled: true,
  schedule_enabled: false,
  interval_minutes: 60,
  created_at: '2026-10-03T01:00:00Z',
  updated_at: '2026-10-03T01:00:00Z',
  ...overrides,
})
