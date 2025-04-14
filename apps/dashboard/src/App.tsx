import { Layout } from '@/containers/Layout';
import { Route, Routes, useNavigate } from 'react-router';
import { isAuthenticated } from '@/lib/auth.ts';
import { useEffect, ReactNode } from 'react';
import { Login } from '@/pages/Login';
import { Toaster } from '@/components/ui/toaster.tsx';
import { Updates } from '@/pages/Updates';
import { Settings } from '@/pages/Settings';
import { Logout } from '@/pages/Logout';

function withLayout(children: ReactNode) {
  return <Layout>{children}</Layout>;
}

export const App = () => {
  const isLoggedIn = isAuthenticated();
  const navigate = useNavigate();

  useEffect(() => {
    if (!isLoggedIn) {
      navigate('/login');
    }
  }, [isLoggedIn, navigate]);

  return (
    <>
      <Toaster />
      <Routes>
        <Route path="/login" element={<Login />} />
        <Route path="/" element={withLayout(<Updates />)} />
        <Route path="/settings" element={withLayout(<Settings />)} />
        <Route path="/logout" element={withLayout(<Logout />)} />
      </Routes>
    </>
  );
};
