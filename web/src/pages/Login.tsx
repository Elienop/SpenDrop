import { useState } from 'react';
import { Link } from 'react-router-dom';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';
import { AlertCircle, WifiOff } from 'lucide-react';
import { useAuth } from '../hooks/useAuth';
import { LogoWordmark } from '@/components/LogoWordmark';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
} from '@/components/ui/card';
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form';
import { Input } from '@/components/ui/input';
import { PasswordInput } from '@/components/ui/password-input';

const loginSchema = z.object({
  username: z.string().min(1, 'Required'),
  password: z.string().min(1, 'Required'),
});

type LoginValues = z.infer<typeof loginSchema>;

export function Login() {
  const { login, user, unverified } = useAuth();
  const [serverError, setServerError] = useState('');
  // The device remembers who was last signed in, but the server has not
  // confirmed it — which is only ever the case when the server cannot be
  // reached. Signing in is impossible in that state, so offer the one thing
  // that still works instead of leaving a dead end.
  const offlineIdentity = unverified && user ? user : null;
  const form = useForm<LoginValues>({
    resolver: zodResolver(loginSchema),
    defaultValues: { username: '', password: '' },
  });

  async function onSubmit(values: LoginValues) {
    setServerError('');
    try {
      await login(values.username, values.password);
    } catch (err) {
      setServerError(err instanceof Error ? err.message : 'Login failed');
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-background p-6">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <h1>
            <LogoWordmark className="h-7" />
            <span className="sr-only">SpenDrop</span>
          </h1>
          <CardDescription>Welcome back</CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-6">
          {offlineIdentity && (
            <div className="flex flex-col gap-3">
              <Alert>
                <WifiOff className="h-4 w-4" />
                <AlertDescription>
                  Can’t reach the server, so signing in isn’t possible right
                  now. You can still add expenses on this device — they’ll sync
                  as soon as you’re back online.
                </AlertDescription>
              </Alert>
              <Button asChild variant="outline" className="w-full">
                <Link to="/quick">
                  Continue as {offlineIdentity.display_name}
                </Link>
              </Button>
            </div>
          )}
          <Form {...form}>
            <form
              onSubmit={(e) => void form.handleSubmit(onSubmit)(e)}
              className="flex flex-col gap-4"
            >
              {serverError && (
                <Alert variant="destructive">
                  <AlertCircle className="h-4 w-4" />
                  <AlertDescription>{serverError}</AlertDescription>
                </Alert>
              )}
              <FormField
                control={form.control}
                name="username"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>Username</FormLabel>
                    <FormControl>
                      <Input autoComplete="username" {...field} />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name="password"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>Password</FormLabel>
                    <FormControl>
                      <PasswordInput
                        autoComplete="current-password"
                        {...field}
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <Button
                type="submit"
                className="w-full"
                disabled={form.formState.isSubmitting}
              >
                {form.formState.isSubmitting ? 'Logging in...' : 'Log in'}
              </Button>
              <p className="text-center text-sm text-muted-foreground">
                No account?{' '}
                <Link
                  to="/register"
                  className="font-medium text-foreground underline-offset-4 hover:underline"
                >
                  Register
                </Link>
              </p>
            </form>
          </Form>
        </CardContent>
      </Card>
    </div>
  );
}
