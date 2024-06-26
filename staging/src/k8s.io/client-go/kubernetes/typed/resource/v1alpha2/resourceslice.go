/*
Copyright The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Code generated by client-gen. DO NOT EDIT.

package v1alpha2

import (
	"context"
	json "encoding/json"
	"fmt"
	"time"

	v1alpha2 "k8s.io/api/resource/v1alpha2"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	types "k8s.io/apimachinery/pkg/types"
	watch "k8s.io/apimachinery/pkg/watch"
	resourcev1alpha2 "k8s.io/client-go/applyconfigurations/resource/v1alpha2"
	scheme "k8s.io/client-go/kubernetes/scheme"
	rest "k8s.io/client-go/rest"
	consistencydetector "k8s.io/client-go/util/consistencydetector"
)

// ResourceSlicesGetter has a method to return a ResourceSliceInterface.
// A group's client should implement this interface.
type ResourceSlicesGetter interface {
	ResourceSlices() ResourceSliceInterface
}

// ResourceSliceInterface has methods to work with ResourceSlice resources.
type ResourceSliceInterface interface {
	Create(ctx context.Context, resourceSlice *v1alpha2.ResourceSlice, opts v1.CreateOptions) (*v1alpha2.ResourceSlice, error)
	Update(ctx context.Context, resourceSlice *v1alpha2.ResourceSlice, opts v1.UpdateOptions) (*v1alpha2.ResourceSlice, error)
	Delete(ctx context.Context, name string, opts v1.DeleteOptions) error
	DeleteCollection(ctx context.Context, opts v1.DeleteOptions, listOpts v1.ListOptions) error
	Get(ctx context.Context, name string, opts v1.GetOptions) (*v1alpha2.ResourceSlice, error)
	List(ctx context.Context, opts v1.ListOptions) (*v1alpha2.ResourceSliceList, error)
	Watch(ctx context.Context, opts v1.ListOptions) (watch.Interface, error)
	Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts v1.PatchOptions, subresources ...string) (result *v1alpha2.ResourceSlice, err error)
	Apply(ctx context.Context, resourceSlice *resourcev1alpha2.ResourceSliceApplyConfiguration, opts v1.ApplyOptions) (result *v1alpha2.ResourceSlice, err error)
	ResourceSliceExpansion
}

// resourceSlices implements ResourceSliceInterface
type resourceSlices struct {
	client rest.Interface
}

// newResourceSlices returns a ResourceSlices
func newResourceSlices(c *ResourceV1alpha2Client) *resourceSlices {
	return &resourceSlices{
		client: c.RESTClient(),
	}
}

// Get takes name of the resourceSlice, and returns the corresponding resourceSlice object, and an error if there is any.
func (c *resourceSlices) Get(ctx context.Context, name string, options v1.GetOptions) (result *v1alpha2.ResourceSlice, err error) {
	result = &v1alpha2.ResourceSlice{}
	err = c.client.Get().
		Resource("resourceslices").
		Name(name).
		VersionedParams(&options, scheme.ParameterCodec).
		Do(ctx).
		Into(result)
	return
}

// List takes label and field selectors, and returns the list of ResourceSlices that match those selectors.
func (c *resourceSlices) List(ctx context.Context, opts v1.ListOptions) (result *v1alpha2.ResourceSliceList, err error) {
	defer func() {
		if err == nil {
			consistencydetector.CheckListFromCacheDataConsistencyIfRequested(ctx, "list request for resourceslices", c.list, opts, result)
		}
	}()
	return c.list(ctx, opts)
}

// list takes label and field selectors, and returns the list of ResourceSlices that match those selectors.
func (c *resourceSlices) list(ctx context.Context, opts v1.ListOptions) (result *v1alpha2.ResourceSliceList, err error) {
	var timeout time.Duration
	if opts.TimeoutSeconds != nil {
		timeout = time.Duration(*opts.TimeoutSeconds) * time.Second
	}
	result = &v1alpha2.ResourceSliceList{}
	err = c.client.Get().
		Resource("resourceslices").
		VersionedParams(&opts, scheme.ParameterCodec).
		Timeout(timeout).
		Do(ctx).
		Into(result)
	return
}

// Watch returns a watch.Interface that watches the requested resourceSlices.
func (c *resourceSlices) Watch(ctx context.Context, opts v1.ListOptions) (watch.Interface, error) {
	var timeout time.Duration
	if opts.TimeoutSeconds != nil {
		timeout = time.Duration(*opts.TimeoutSeconds) * time.Second
	}
	opts.Watch = true
	return c.client.Get().
		Resource("resourceslices").
		VersionedParams(&opts, scheme.ParameterCodec).
		Timeout(timeout).
		Watch(ctx)
}

// Create takes the representation of a resourceSlice and creates it.  Returns the server's representation of the resourceSlice, and an error, if there is any.
func (c *resourceSlices) Create(ctx context.Context, resourceSlice *v1alpha2.ResourceSlice, opts v1.CreateOptions) (result *v1alpha2.ResourceSlice, err error) {
	result = &v1alpha2.ResourceSlice{}
	err = c.client.Post().
		Resource("resourceslices").
		VersionedParams(&opts, scheme.ParameterCodec).
		Body(resourceSlice).
		Do(ctx).
		Into(result)
	return
}

// Update takes the representation of a resourceSlice and updates it. Returns the server's representation of the resourceSlice, and an error, if there is any.
func (c *resourceSlices) Update(ctx context.Context, resourceSlice *v1alpha2.ResourceSlice, opts v1.UpdateOptions) (result *v1alpha2.ResourceSlice, err error) {
	result = &v1alpha2.ResourceSlice{}
	err = c.client.Put().
		Resource("resourceslices").
		Name(resourceSlice.Name).
		VersionedParams(&opts, scheme.ParameterCodec).
		Body(resourceSlice).
		Do(ctx).
		Into(result)
	return
}

// Delete takes name of the resourceSlice and deletes it. Returns an error if one occurs.
func (c *resourceSlices) Delete(ctx context.Context, name string, opts v1.DeleteOptions) error {
	return c.client.Delete().
		Resource("resourceslices").
		Name(name).
		Body(&opts).
		Do(ctx).
		Error()
}

// DeleteCollection deletes a collection of objects.
func (c *resourceSlices) DeleteCollection(ctx context.Context, opts v1.DeleteOptions, listOpts v1.ListOptions) error {
	var timeout time.Duration
	if listOpts.TimeoutSeconds != nil {
		timeout = time.Duration(*listOpts.TimeoutSeconds) * time.Second
	}
	return c.client.Delete().
		Resource("resourceslices").
		VersionedParams(&listOpts, scheme.ParameterCodec).
		Timeout(timeout).
		Body(&opts).
		Do(ctx).
		Error()
}

// Patch applies the patch and returns the patched resourceSlice.
func (c *resourceSlices) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts v1.PatchOptions, subresources ...string) (result *v1alpha2.ResourceSlice, err error) {
	result = &v1alpha2.ResourceSlice{}
	err = c.client.Patch(pt).
		Resource("resourceslices").
		Name(name).
		SubResource(subresources...).
		VersionedParams(&opts, scheme.ParameterCodec).
		Body(data).
		Do(ctx).
		Into(result)
	return
}

// Apply takes the given apply declarative configuration, applies it and returns the applied resourceSlice.
func (c *resourceSlices) Apply(ctx context.Context, resourceSlice *resourcev1alpha2.ResourceSliceApplyConfiguration, opts v1.ApplyOptions) (result *v1alpha2.ResourceSlice, err error) {
	if resourceSlice == nil {
		return nil, fmt.Errorf("resourceSlice provided to Apply must not be nil")
	}
	patchOpts := opts.ToPatchOptions()
	data, err := json.Marshal(resourceSlice)
	if err != nil {
		return nil, err
	}
	name := resourceSlice.Name
	if name == nil {
		return nil, fmt.Errorf("resourceSlice.Name must be provided to Apply")
	}
	result = &v1alpha2.ResourceSlice{}
	err = c.client.Patch(types.ApplyPatchType).
		Resource("resourceslices").
		Name(*name).
		VersionedParams(&patchOpts, scheme.ParameterCodec).
		Body(data).
		Do(ctx).
		Into(result)
	return
}
