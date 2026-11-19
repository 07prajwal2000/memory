import React from 'react';
import { useRouteError, isRouteErrorResponse, useNavigate } from 'react-router-dom';
import { Box, Button } from '@mantine/core';
import { ErrorState } from '../components';

export function RouteErrorBoundary() {
  const error = useRouteError();
  const navigate = useNavigate();

  let title = 'An unexpected error occurred';
  let message = 'Something went wrong while rendering this page.';

  if (isRouteErrorResponse(error)) {
    if (error.status === 404) {
      title = 'Page Not Found';
      message = "The page you're looking for does not exist or has been moved.";
    } else {
      title = `Error ${error.status}: ${error.statusText}`;
      message = error.data?.message || message;
    }
  } else if (error instanceof Error) {
    message = error.message;
  }

  return (
    <Box p="xl" maw={800} mx="auto">
      <ErrorState
        title={title}
        message={message}
        action={
          <Button
            variant="default"
            size="sm"
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

interface ClassErrorBoundaryProps {
  children: React.ReactNode;
}

interface ClassErrorBoundaryState {
  hasError: boolean;
  error?: Error;
}

export class AppErrorBoundary extends React.Component<
  ClassErrorBoundaryProps,
  ClassErrorBoundaryState
> {
  constructor(props: ClassErrorBoundaryProps) {
    super(props);
    this.state = { hasError: false };
  }

  static getDerivedStateFromError(error: Error): ClassErrorBoundaryState {
    return { hasError: true, error };
  }

  componentDidCatch(error: Error, errorInfo: React.ErrorInfo) {
    console.error('AppErrorBoundary caught an error:', error, errorInfo);
  }

  render() {
    if (this.state.hasError) {
      return (
        <Box p="xl" maw={800} mx="auto">
          <ErrorState
            title="Application Error"
            message={this.state.error?.message || 'An unexpected error occurred.'}
            onRetry={() => window.location.reload()}
          />
        </Box>
      );
    }

    return this.props.children;
  }
}
