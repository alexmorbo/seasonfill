import { describe, expect, it } from 'vitest';
import { httpURL, pickPublicHref } from './url';

describe('httpURL', () => {
  it('passes http and https through unchanged', () => {
    expect(httpURL('http://sonarr:8989')).toBe('http://sonarr:8989');
    expect(httpURL('https://sonarr.example.com'))
      .toBe('https://sonarr.example.com');
  });

  it('trims surrounding whitespace before validating', () => {
    expect(httpURL('  https://sonarr.example.com  '))
      .toBe('https://sonarr.example.com');
  });

  it('returns null for bare hostnames', () => {
    expect(httpURL('sonarr')).toBeNull();
    expect(httpURL('sonarr.example.com')).toBeNull();
  });

  it('returns null for empty and nullish inputs', () => {
    expect(httpURL('')).toBeNull();
    expect(httpURL('   ')).toBeNull();
    expect(httpURL(null)).toBeNull();
    expect(httpURL(undefined)).toBeNull();
  });

  it('rejects non-http schemes', () => {
    expect(httpURL('ftp://sonarr.example.com')).toBeNull();
    expect(httpURL('javascript:alert(1)')).toBeNull();
  });

  it('is case-insensitive on the scheme', () => {
    expect(httpURL('HTTPS://Sonarr.Example.COM'))
      .toBe('HTTPS://Sonarr.Example.COM');
  });
});

describe('pickPublicHref', () => {
  it('prefers a valid public URL over a valid internal URL', () => {
    expect(pickPublicHref(
      'https://sonarr.example.com',
      'http://sonarr:8989',
    )).toBe('https://sonarr.example.com');
  });

  it('falls back to internal URL when public is missing', () => {
    expect(pickPublicHref(undefined, 'http://sonarr:8989'))
      .toBe('http://sonarr:8989');
    expect(pickPublicHref('', 'http://sonarr:8989'))
      .toBe('http://sonarr:8989');
  });

  it('falls back to internal when public is schemeless', () => {
    expect(pickPublicHref('sonarr.example.com', 'http://sonarr:8989'))
      .toBe('http://sonarr:8989');
  });

  it('returns null when neither produces a real http(s) URL', () => {
    expect(pickPublicHref('sonarr', 'sonarr')).toBeNull();
    expect(pickPublicHref(undefined, undefined)).toBeNull();
    expect(pickPublicHref('', '')).toBeNull();
  });
});
