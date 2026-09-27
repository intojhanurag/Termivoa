import { describe, expect, it } from 'vitest';
import { offlineRetry } from './App';

describe('offlineRetry', () => {
  it('keeps "connecting" for the first failure, then shows offline', () => {
    expect(offlineRetry(1).offline).toBe(false);
    expect(offlineRetry(2).offline).toBe(true);
    expect(offlineRetry(9).offline).toBe(true);
  });

  it('backs off 1, 2, 4, 8 seconds, then retries every 10', () => {
    expect([1, 2, 3, 4, 5, 6, 50].map((failures) => offlineRetry(failures).retryIn)).toEqual([1, 2, 4, 8, 10, 10, 10]);
  });
});
