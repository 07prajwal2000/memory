import { Box, Button } from '@mantine/core';
import { IconNote, IconPlus } from '@tabler/icons-react';
import { useParams, useNavigate } from 'react-router-dom';
import { EmptyState } from '../../components';

export function MemoryEmptyDetailView() {
  const { slug = 'global' } = useParams<{ slug?: string }>();
  const navigate = useNavigate();

  return (
    <Box
      style={{
        height: '100%',
        minHeight: '400px',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        padding: '24px',
      }}
    >
      <EmptyState
        icon={<IconNote size={48} stroke={1.5} />}
        title="Select a memory"
        description="Choose a memory from the sidebar to view or edit, or create a new one."
        action={
          <Button
            color="accent"
            size="md"
            leftSection={<IconPlus size={18} />}
            onClick={() => navigate(`/c/${slug}/new`)}
          >
            New Memory
          </Button>
        }
      />
    </Box>
  );
}
