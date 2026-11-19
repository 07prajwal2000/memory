import { describe, it, expect } from 'vitest';
import { screen, waitFor, fireEvent } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '../../test/test-utils';
import { ContextsNavList } from './ContextsNavList';
import { CreateContextModal } from './CreateContextModal';
import { ExpiryBadge } from './ExpiryBadge';
import { mockDb } from '../../mocks/db';

describe('Contexts Feature', () => {
  describe('ExpiryBadge', () => {
    it('renders null when expiresAt is null (permanent)', () => {
      renderWithProviders(<ExpiryBadge expiresAt={null} />);
      expect(screen.queryByText(/expires/i)).not.toBeInTheDocument();
    });

    it('renders warning color badge when expiring in less than 24h', () => {
      const soon = new Date(Date.now() + 6 * 3600000).toISOString();
      renderWithProviders(<ExpiryBadge expiresAt={soon} />);
      expect(screen.getByText(/expires in/i)).toBeInTheDocument();
    });

    it('renders expired badge when past expiry', () => {
      const past = new Date(Date.now() - 3600000).toISOString();
      renderWithProviders(<ExpiryBadge expiresAt={past} />);
      expect(screen.getByText('expired')).toBeInTheDocument();
    });
  });

  describe('ContextsNavList sidebar', () => {
    it('renders global context pinned first, followed by default file entry and other contexts', async () => {
      renderWithProviders(<ContextsNavList />);

      // Wait for contexts to load
      await waitFor(() => {
        expect(screen.getByText('global')).toBeInTheDocument();
      });

      expect(screen.getByText('Default file')).toBeInTheDocument();
      expect(screen.getByText('global/readme.md')).toBeInTheDocument();
      expect(screen.getByText('core-system')).toBeInTheDocument();
      expect(screen.getByText('devops-infra')).toBeInTheDocument();
      expect(screen.getByText('tmp-debug-investigation')).toBeInTheDocument();
    });

    it('displays temporary context with expiry badge', async () => {
      renderWithProviders(<ContextsNavList />);

      await waitFor(() => {
        expect(screen.getByText('tmp-debug-investigation')).toBeInTheDocument();
      });

      expect(screen.getByText(/expires in/i)).toBeInTheDocument();
    });
  });

  describe('CreateContextModal flow', () => {
    it('creates a permanent context with valid slug', async () => {
      const user = userEvent.setup();
      renderWithProviders(<CreateContextModal opened={true} onClose={() => {}} />);

      const slugInput = screen.getByLabelText(/context slug/i);
      await user.type(slugInput, 'analytics-pipeline');

      const submitBtn = screen.getByRole('button', { name: /create context/i });
      await user.click(submitBtn);

      await waitFor(() => {
        const ctx = mockDb.getContext('analytics-pipeline');
        expect(ctx).toBeDefined();
        expect(ctx.slug).toBe('analytics-pipeline');
        expect(ctx.expires_at).toBeNull();
      });
    });

    it('creates an auto-named temporary context when slug is left empty', async () => {
      const user = userEvent.setup();
      renderWithProviders(<CreateContextModal opened={true} onClose={() => {}} />);

      const submitBtn = screen.getByRole('button', { name: /create auto temp context/i });
      await user.click(submitBtn);

      await waitFor(() => {
        const contexts = mockDb.listContexts();
        const autoTemp = contexts.find((c) => c.slug.startsWith('tmp-'));
        expect(autoTemp).toBeDefined();
        expect(autoTemp?.expires_at).not.toBeNull();
      });
    });

    it('shows validation error for invalid slug', async () => {
      renderWithProviders(<CreateContextModal opened={true} onClose={() => {}} />);

      const slugInput = screen.getByLabelText(/context slug/i);
      fireEvent.change(slugInput, { target: { value: 'Invalid_Slug!' } });

      await waitFor(() => {
        expect(screen.getByText(/slug must contain only lowercase letters/i)).toBeInTheDocument();
      });
    });

    it('shows error when creating context with duplicate slug', async () => {
      const user = userEvent.setup();
      renderWithProviders(<CreateContextModal opened={true} onClose={() => {}} />);

      const slugInput = screen.getByLabelText(/context slug/i);
      await user.type(slugInput, 'core-system'); // already exists

      const submitBtn = screen.getByRole('button', { name: /create context/i });
      await user.click(submitBtn);

      await waitFor(() => {
        expect(screen.getByText(/already exists/i)).toBeInTheDocument();
      });
    });
  });
});
