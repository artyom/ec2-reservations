// Command ec2-reservations reports mismatch of running on-demand ec2 instances
// and number of reserved instances. It does not take into account additional
// instance attributes like Linux/non-linux, VPC/non-VPC, it only matches
// instances/reservations based on type (like m3.medium) and availability zone
// (in case of AZ-scoped reservations).
//
// Use regular AWS SDK variables to set authentication and region:
// AWS_SECRET_KEY, AWS_ACCESS_KEY, AWS_REGION.
package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
)

func main() {
	if err := do(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func do(w io.Writer) error {
	sess, err := session.NewSession()
	if err != nil {
		return err
	}
	svc := ec2.New(sess)
	resp, err := svc.DescribeInstances(&ec2.DescribeInstancesInput{
		Filters: []*ec2.Filter{{
			Name:   aws.String("instance-state-name"),
			Values: []*string{aws.String("running")},
		}},
	})
	if err != nil {
		return err
	}
	runningInstances := make(map[instanceInfo]int)
	for _, r := range resp.Reservations {
		for _, inst := range r.Instances {
			ii := instanceInfo{Type: *inst.InstanceType, AZ: *inst.Placement.AvailabilityZone}
			runningInstances[ii] += 1
		}
	}

	ris, err := svc.DescribeReservedInstances(&ec2.DescribeReservedInstancesInput{
		Filters: []*ec2.Filter{{
			Name:   aws.String("state"),
			Values: []*string{aws.String("active")},
		}},
	})
	if err != nil {
		return err
	}
	// Match these:
	// InstanceType: "t2.xlarge",
	// InstanceCount: 1,
	//
	// Two possible cases:
	// 1.  Scope: "Availability Zone", AvailabilityZone: "us-east-1e",
	// 2.  Scope: "Region",
	azReservations := make(map[instanceInfo]int)
	regionReservations := make(map[instanceInfo]int)
	for _, r := range ris.ReservedInstances {
		switch *r.Scope {
		case "Region":
			ii := instanceInfo{Type: *r.InstanceType}
			regionReservations[ii] += int(*r.InstanceCount)
		case "Availability Zone":
			ii := instanceInfo{Type: *r.InstanceType, AZ: *r.AvailabilityZone}
			azReservations[ii] += int(*r.InstanceCount)
		default:
			return fmt.Errorf("unknown reservation scope: %q", *r.Scope)
		}
	}
	var onDemandInstances []reportedInfo
	var unusedReservations []reportedInfo
	for k, v := range reconcile(runningInstances, azReservations, regionReservations) {
		switch {
		case v < 0:
			ri := reportedInfo{Type: k.Type, AZ: k.AZ, Count: -v}
			onDemandInstances = append(onDemandInstances, ri)
		case v > 0:
			ri := reportedInfo{Type: k.Type, Count: v}
			unusedReservations = append(unusedReservations, ri)
		}
	}
	sort.SliceStable(onDemandInstances,
		func(i, j int) bool { return onDemandInstances[i].Type < onDemandInstances[j].Type })
	sort.SliceStable(unusedReservations,
		func(i, j int) bool { return unusedReservations[i].Type < unusedReservations[j].Type })
	tw := tabwriter.NewWriter(w, 0, 8, 1, '\t', 0)
	if len(onDemandInstances) > 0 {
		fmt.Fprintln(tw, "On-demand EC2 instances:")
	}
	for _, v := range onDemandInstances {
		fmt.Fprintf(tw, "%s\t%d\t%s\n", v.Type, v.Count, v.AZ)
	}
	if len(unusedReservations) > 0 {
		fmt.Fprintln(tw, "Unused reservations:")
	}
	for _, v := range unusedReservations {
		fmt.Fprintf(tw, "%s\t%d\n", v.Type, v.Count)
	}
	return tw.Flush()
}

type instanceInfo struct {
	Type string
	AZ   string
}

type reportedInfo struct {
	Type  string
	AZ    string
	Count int
}

// algorithm:
// 1. fetch all reserved instances info, put them into 2 maps: one for AZ-scoped
// reservations, one for Region-scoped reservations. Key of map is a struct,
// value is number of instances.
// 2. fetch all running instances info
// 3. for each running instance info decrease number in AZ-scoped reservations,
// so that final form of AZ-scoped reservations would contain positive values
// for unused reservations, and negative values for running instances w/o
// reservations.
// 4. iterate over k/v pairs with NEGATIVE values in AZ-scoped map, try to add
// values from Region-scoped reservations map.

func reconcile(runningInstances, azReservations, regionReservations map[instanceInfo]int) map[instanceInfo]int {
	out := make(map[instanceInfo]int, len(runningInstances))
	for k, v := range runningInstances {
		out[k] = -v
	}
	for k, v := range azReservations {
		out[k] += v
	}
	for k, v := range out {
		if v >= 0 { // only process items that really lacks reservations
			continue
		}
		// fmt.Printf("k=%v, v=%d\n", k, v)
		k2 := instanceInfo{Type: k.Type}
		if v2, ok := regionReservations[k2]; ok {
			need, have := -v, v2
			switch {
			case need >= have:
				out[k] = v + v2
				delete(regionReservations, k2)
			default:
				n := have - need
				out[k] = v + n
				regionReservations[k2] = v2 - n
			}
			// fmt.Printf("k=%v, v=%d, v2=%d\n", k, v, v2)
		}
	}
	for k, v := range regionReservations {
		out[k] = v
	}
	return out
}
