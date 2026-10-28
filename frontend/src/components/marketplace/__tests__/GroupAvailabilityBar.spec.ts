import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import GroupAvailabilityBar from '../GroupAvailabilityBar.vue'
import type { MarketplaceGroupAvailability } from '@/api/marketplace.types'

// GroupAvailabilityBar.spec.ts verifies sampled passive health inside group
// cards. Server assessments deliberately differ from raw success ratios so
// historical colors cannot silently revert to the former availability bands.

vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key }),
}))

// A low raw ratio can coexist with healthy scoring because ignored client
// failures remain in success ratios. The card must trust the sampled assessment.
describe('GroupAvailabilityBar', () => {
  // Configured health excludes client failures while retaining their raw ratio.
  // Both the visible status and history color follow that server assessment.
  it('uses sampled health rather than raw success thresholds', () => {
    const availability: MarketplaceGroupAvailability = {
      window_days: 1,
      bucket_minutes: 1440,
      success_count: 50,
      total_count: 100,
      availability_rate: 0.5,
      health: { status: 'healthy', score: 100, ttft_p50_ms: 1250, cache_rate: 0.58 },
      days: [
        {
          date: '2026-10-28T00:00:00Z',
          success_count: 50,
          total_count: 100,
          availability_rate: 0.5,
          health: { status: 'healthy', score: 100, ttft_p50_ms: 1250, cache_rate: 0.58 },
        },
      ],
    }
    const wrapper = mount(GroupAvailabilityBar, { props: { availability } })
    expect(wrapper.get('[data-testid="passive-health-status"]').text()).toContain(
      'marketplace.monitorStates.healthy',
    )
    expect(wrapper.get('[data-testid="passive-health-bucket"]').classes()).toContain(
      'bg-emerald-500',
    )
    expect(wrapper.text()).toContain('50.00%')
    expect(wrapper.text()).toContain('1.25 s')
    expect(wrapper.get('[data-testid="passive-cache-rate"]').text()).toContain('58.00%')
    expect(wrapper.get('[data-testid="passive-health-bucket"]').attributes('title')).toContain(
      '58.00%',
    )
    wrapper.unmount()
  })

  // An older response has a perfect rate but no sample-gated health metadata.
  // Missing and explicitly unknown cells remain neutral, including padded history.
  it('keeps unsampled and absent health unknown', () => {
    const wrapper = mount(GroupAvailabilityBar, {
      props: {
        availability: {
          window_days: 1,
          bucket_minutes: 720,
          success_count: 1,
          total_count: 1,
          availability_rate: 1,
          days: [
            {
              date: '2026-10-28T00:00:00Z',
              success_count: 1,
              total_count: 1,
              availability_rate: 1,
              health: { status: 'unknown' },
            },
          ],
        },
      },
    })
    expect(wrapper.get('[data-testid="passive-health-status"]').text()).toContain(
      'marketplace.monitorStates.unknown',
    )
    for (const bucket of wrapper.findAll('[data-testid="passive-health-bucket"]')) {
      expect(bucket.classes()).toContain('bg-gray-200')
    }
    expect(wrapper.text()).toContain('—')
    expect(wrapper.get('[data-testid="passive-cache-rate"]').text()).toContain(
      'marketplace.monitorStates.unknown',
    )
    wrapper.unmount()
  })

  // Zero cache reads are a measured result while absent cache data is unknown.
  // The card must retain a known zero percentage instead of hiding the signal.
  it('shows a measured zero cache hit rate', () => {
    const wrapper = mount(GroupAvailabilityBar, {
      props: {
        availability: {
          window_days: 1,
          bucket_minutes: 1440,
          success_count: 50,
          total_count: 50,
          health: { status: 'healthy', cache_rate: 0 },
          days: [],
        },
      },
    })
    expect(wrapper.get('[data-testid="passive-cache-rate"]').text()).toContain('0.00%')
    wrapper.unmount()
  })

  // Aggregation can be delayed even when the last measured channel was healthy.
  // The card reports delayed data separately and retains the recorded assessment.
  it('distinguishes delayed aggregation from channel failure', () => {
    const wrapper = mount(GroupAvailabilityBar, {
      props: {
        availability: {
          window_days: 1,
          bucket_minutes: 1440,
          success_count: 50,
          total_count: 50,
          availability_rate: 1,
          health: { status: 'healthy' },
          days: [],
          data_through: new Date(Date.now() - 3600000).toISOString(),
          refresh_interval_seconds: 300,
          coverage_complete: false,
        },
      },
    })
    expect(wrapper.text()).toContain('marketplace.monitorDelayed')
    expect(wrapper.get('[data-testid="passive-health-status"]').text()).toContain(
      'marketplace.monitorStates.healthy',
    )
    wrapper.unmount()
  })
})
