import { Box, Button } from '@mantine/core';
import { useNavigate } from 'react-router-dom';
import { ErrorState } from '../components';

export function NotFoundView() {
  const navigate = useNavigate();

  return (
    <Box p="xl" maw={800} mx="auto">
      <ErrorState
        title="Page Not Found (404)"
        message="The page you're looking for does not exist or has been moved."
        action={
          <Button
            variant="default"
            size="md"
            style={{ minHeight: 44 }}
            onClick={() => navigate('/c/global')}
            mt="xs"
          >
            Back to Global Context
          </Button>
        }
      />
    </Box>
  );
}
